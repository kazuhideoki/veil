package usecase

import (
	"fmt"
	"strings"

	"github.com/kazuhideoki/veil/internal/domain"
)

const targetRefSeparator = ":"

type targetSelection struct {
	workspaceID       string
	workspace         domain.Workspace
	target            string
	explicitWorkspace bool
}

// resolveTargetSelection accepts either a current-workspace path or workspace_id:target.
func resolveTargetSelection(fs workspaceResolverFileSystem, config domain.Config, ref string) (targetSelection, error) {
	workspaceID := ""
	target := ref
	explicitWorkspace := false

	if id, targetPath, found := strings.Cut(ref, targetRefSeparator); found {
		if id == "" || targetPath == "" {
			return targetSelection{}, fmt.Errorf("invalid target ref %q; expected workspace_id:target", ref)
		}
		workspaceID = id
		target = targetPath
		explicitWorkspace = true
	}

	var workspace domain.Workspace
	if explicitWorkspace {
		var ok bool
		workspace, ok = config.Workspaces[workspaceID]
		if !ok {
			return targetSelection{}, fmt.Errorf("workspace is not registered: %s", workspaceID)
		}
	} else {
		var err error
		workspaceID, workspace, err = currentWorkspace(config, fs)
		if err != nil {
			return targetSelection{}, err
		}
	}

	target, err := normalizeEditTargetPath(target)
	if err != nil {
		return targetSelection{}, err
	}
	if !hasTarget(workspace.Targets, target) {
		return targetSelection{}, fmt.Errorf("target is not registered: %s", formatTargetRef(workspaceID, target))
	}

	return targetSelection{
		workspaceID:       workspaceID,
		workspace:         workspace,
		target:            target,
		explicitWorkspace: explicitWorkspace,
	}, nil
}

func formatTargetRef(workspaceID, target string) string {
	return workspaceID + targetRefSeparator + target
}
