//go:build windows

package proctree

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Tree is a job object holding the shell and every process it starts.
// The job is created with KILL_ON_JOB_CLOSE, so closing it ends leftovers.
// The shell is started CREATE_SUSPENDED and resumed only after it has been
// assigned to the job: a child the shell starts in a window between Start and
// the assignment would be outside the job, escaping both the kill and the
// resource limits.
type Tree struct {
	cmd *exec.Cmd
	job windows.Handle
	// exceeded records a job limit ending a process, read before the job
	// handle is closed (Close runs right after Wait).
	exceeded bool
}

// jobBasicAccounting mirrors JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, which
// x/sys/windows does not declare.
type jobBasicAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	// TotalTerminatedProcesses counts the processes the job ended because one
	// of its limits was hit; TerminateJobObject does not count as one.
	TotalTerminatedProcesses uint32
}

// New prepares cmd (before Start) so its whole process tree can be killed.
func New(cmd *exec.Cmd) *Tree { return NewLimited(cmd, Limits{}) }

// NewLimited is New with the resource caps on the job object: memory (one
// process and the whole tree), active processes and CPU time per process. A
// process over the memory limit gets its allocation refused (STATUS_COMMITMENT_
// LIMIT); a process over the count limit cannot be created; a process over the
// CPU time limit is ended by the system and counted by Exceeded.
func NewLimited(cmd *exec.Cmd, limits Limits) *Tree {
	p := &Tree{cmd: cmd}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return p // fall back to killing the shell alone
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if limits.MemoryBytes > 0 {
		// Both caps: one process may not commit more than the limit, and the
		// tree as a whole may not either (a command forking a hundred children
		// hits the job cap the way one big allocation hits the process cap).
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY | windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		info.ProcessMemoryLimit = uintptr(limits.MemoryBytes)
		info.JobMemoryLimit = uintptr(limits.MemoryBytes)
	}
	if limits.Processes > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
		info.BasicLimitInformation.ActiveProcessLimit = uint32(limits.Processes)
	}
	if limits.CPUTime > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_PROCESS_TIME
		info.BasicLimitInformation.PerProcessUserTimeLimit = limits.CPUTime.Nanoseconds() / 100 // 100ns units
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return p
	}
	p.job = job
	// With a job to enter, start the shell suspended and resume it in Started
	// only after the assignment: while suspended the shell can start nothing, so
	// a child cannot end up outside the job. Without a job the shell is started
	// normally and only the shell itself can be killed (as before).
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	return p
}

// Started assigns the suspended shell to the job (children inherit it) and then
// resumes it. The caller must start the command right away and call this right
// after Start: until Started succeeds the shell cannot run. When the assignment
// or the resume fails, the still-suspended shell is terminated so that nothing
// ever runs outside the job, and the error is returned for the caller to report
// the command as failed.
func (p *Tree) Started() error {
	if p.cmd.Process == nil {
		return nil
	}
	if p.job == 0 {
		// No job (creating it failed): the process started normally, not
		// suspended, and the shell alone can still be killed.
		return nil
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.cmd.Process.Pid))
	if err != nil {
		p.abort()
		return fmt.Errorf("assign the shell to its job: open pid %d: %w", p.cmd.Process.Pid, err)
	}
	err = windows.AssignProcessToJobObject(p.job, h)
	_ = windows.CloseHandle(h)
	if err != nil {
		p.abort()
		return fmt.Errorf("assign the shell to its job: %w", err)
	}
	if err := resumeProcess(uint32(p.cmd.Process.Pid)); err != nil {
		p.abort()
		return fmt.Errorf("resume the suspended shell: %w", err)
	}
	return nil
}

// abort terminates a shell that could not join the job or be resumed, so it
// never starts (and never starts children) outside the job's limits.
func (p *Tree) abort() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// resumeProcess resumes the threads of a process started with
// CREATE_SUSPENDED. Such a process holds just its initial thread (suspended,
// so it can have created no others); os.StartProcess does not return that
// thread's handle, so the threads are listed with the tool help snapshot and
// each one opened and resumed.
func resumeProcess(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snap)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snap, &entry); err != nil {
		return err
	}
	resumed := 0
	for {
		if entry.OwnerProcessID == pid {
			th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			_, err = windows.ResumeThread(th)
			_ = windows.CloseHandle(th)
			if err != nil {
				return err
			}
			resumed++
		}
		if err := windows.Thread32Next(snap, &entry); err != nil {
			// The enumeration ends with ERROR_NO_MORE_FILES; anything else on a
			// snapshot of a mid flight process is not expected, and a thread that
			// was never found and resumed keeps the process dead until Kill.
			break
		}
	}
	if resumed == 0 {
		return errors.New("no thread of the process in the snapshot")
	}
	return nil
}

// Kill terminates the whole job, and the shell directly in case it was not
// assigned yet.
func (p *Tree) Kill() error {
	if p.job != 0 {
		_ = windows.TerminateJobObject(p.job, 1)
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return nil
}

// Exceeded reports whether the system ended a process of the job because it hit
// one of the limits. A refused allocation does not end anything (the process
// fails on its own), so the tool reads the output as well.
func (p *Tree) Exceeded() bool { return p.exceeded }

// Close releases the job; KILL_ON_JOB_CLOSE ends background leftovers.
func (p *Tree) Close() {
	if p.job != 0 {
		p.exceeded = p.limitTerminated()
		_ = windows.CloseHandle(p.job)
		p.job = 0
	}
}

// limitTerminated asks the job accounting whether a limit ended a process.
func (p *Tree) limitTerminated() bool {
	var acct jobBasicAccounting
	err := windows.QueryInformationJobObject(p.job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&acct)), uint32(unsafe.Sizeof(acct)), nil)
	return err == nil && acct.TotalTerminatedProcesses > 0
}
