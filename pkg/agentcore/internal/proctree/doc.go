// Package proctree kills a command's whole process tree: a process group on
// Unix, a job object (KILL_ON_JOB_CLOSE) on Windows, where the shell starts
// suspended and is resumed only after joining the job. Call New before Start,
// Started right after Start (it ends a shell that could not join), Kill to end
// the tree, Close once the command has exited (it also ends children left
// running in the background).
package proctree
