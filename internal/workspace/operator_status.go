package workspace

import "os"

type OperatorNode struct {
	Session, Window, Top, Left int
	Pane                       string
	Status                     NodeStatus
}

func readOperatorRuntime(ws *os.Root, owner Owner) (runtimeRecord, error) {
	var saved runtimeRecord
	dir, err := privateDir(ws, "runtime", false)
	if err != nil {
		return saved, err
	}
	defer func() { _ = dir.Close() }()
	if readJSON(dir, "binding.json", &saved) != nil || saved.Version != 1 || !saved.Owner.valid() ||
		ValidateBindings(saved.Bindings) != nil || !sameRuntime(saved.Owner, owner) {
		return saved, ErrUnknown
	}
	for _, binding := range saved.Bindings {
		if !sameRuntime(saved.Owner, binding.Owner) {
			return saved, ErrUnknown
		}
	}
	return saved, nil
}
