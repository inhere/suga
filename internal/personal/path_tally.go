package personal

// pathTally is the one place that folds per-snapshot-entry outcomes into
// per-path outcomes. A path written by several turns (or several times in one
// turn) has several entries; the user and the model care about files, so the
// counts and the path lists count a path once. The entry executed last decides
// the path's outcome: it is the oldest snapshot, i.e. the content the path
// ends with. The per-entry results (RewindResult.Files, checkpoint states) are
// not touched by this.
type pathTally struct {
	order  []string // paths in first-seen order
	final  map[string]RewindFileItem
	counts map[RewindFileAction]int
}

func newPathTally() *pathTally {
	return &pathTally{final: map[string]RewindFileItem{}, counts: map[RewindFileAction]int{}}
}

// add records the outcome of one entry; a later entry of the same path replaces
// the earlier outcome (and its count).
func (t *pathTally) add(item RewindFileItem) {
	if prev, ok := t.final[item.Path]; ok {
		t.counts[prev.Action]--
	} else {
		t.order = append(t.order, item.Path)
	}
	t.final[item.Path] = item
	t.counts[item.Action]++
}

func (t *pathTally) count(action RewindFileAction) int { return t.counts[action] }

// items returns the final outcome of each path, in first-seen order.
func (t *pathTally) items() []RewindFileItem {
	out := make([]RewindFileItem, 0, len(t.order))
	for _, p := range t.order {
		out = append(out, t.final[p])
	}
	return out
}

// paths returns the paths whose final outcome is one of actions, in first-seen order.
func (t *pathTally) paths(actions ...RewindFileAction) []string {
	var out []string
	for _, p := range t.order {
		for _, a := range actions {
			if t.final[p].Action == a {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// DedupeRewindFiles reduces per-entry rewind file results to one row per path
// (the last entry of a path wins), for display. The input is not modified.
func DedupeRewindFiles(items []RewindFileItem) []RewindFileItem {
	t := newPathTally()
	for _, it := range items {
		t.add(it)
	}
	return t.items()
}

// checkpointToFileAction maps an undo outcome onto the rewind vocabulary; any
// other action is a skip (reported as a conflict).
func checkpointToFileAction(a CheckpointAction) RewindFileAction {
	switch a {
	case CheckpointActionRestore:
		return RewindFileRestore
	case CheckpointActionDelete:
		return RewindFileDelete
	case CheckpointActionUnrecoverable:
		return RewindFileUnrecoverable
	default:
		return RewindFileConflict
	}
}

// sync writes the tally into the counts and Files of an UndoResult.
func (t *pathTally) sync(r *UndoResult) {
	r.Restored = t.count(RewindFileRestore)
	r.Deleted = t.count(RewindFileDelete)
	r.Skipped = t.count(RewindFileConflict)
	r.Unrecoverable = t.count(RewindFileUnrecoverable)
	r.Files = t.paths(RewindFileRestore, RewindFileDelete)
}
