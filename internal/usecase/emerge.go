package usecase

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

type emergeFileSystem interface {
	UserHomeDir() (string, error)
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
	MkdirAll(path string, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	Rename(oldpath, newpath string) error
	Stat(name string) (os.FileInfo, error)
	Lstat(name string) (os.FileInfo, error)
	Readlink(name string) (string, error)
	Symlink(oldname, newname string) error
	Remove(name string) error
}

type workspaceResolverFileSystem interface {
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
}

type EmergeTargets struct {
	FileSystem      emergeFileSystem
	DocumentRuntime OnePasswordDocumentRuntime
	Stdout          io.Writer
	VerboseOutput   io.Writer
	Now             func() time.Time
	AllWorkspaces   bool
	TargetRef       string
	OverwriteLocal  bool
}

type emergeWorkspace struct {
	id        string
	workspace domain.Workspace
}

func (u EmergeTargets) Run() error {
	configPath, config, workspaces, err := u.resolveWorkspaces()
	if err != nil {
		return err
	}

	statePath, state, err := loadState(u.FileSystem)
	if err != nil {
		return err
	}

	now := currentTime(u.Now)

	return u.emergeOnePasswordDocuments(configPath, config, statePath, &state, workspaces, now)
}

// ValidateWorkspaceSelection rejects invalid workspace flags before commands cause side effects.
func (u EmergeTargets) ValidateWorkspaceSelection() error {
	_, _, _, err := u.resolveWorkspaces()
	return err
}

func (u EmergeTargets) resolveWorkspaces() (string, domain.Config, []emergeWorkspace, error) {
	if u.OverwriteLocal && (u.AllWorkspaces || u.TargetRef == "") {
		return "", domain.Config{}, nil, fmt.Errorf("emerge --overwrite-local requires exactly one target ref")
	}
	configPath, config, err := loadConfig(u.FileSystem)
	if err != nil {
		return "", domain.Config{}, nil, err
	}

	homeDir, err := u.FileSystem.UserHomeDir()
	if err != nil {
		return "", domain.Config{}, nil, fmt.Errorf("resolve home directory: %w", err)
	}

	config = expandConfigPaths(config, homeDir)
	config = canonicalizeWorkspaceRoots(config, u.FileSystem)
	if err := requireOnePasswordConfig(config); err != nil {
		return "", domain.Config{}, nil, err
	}

	workspaces, err := resolveEmergeWorkspaces(u.FileSystem, config, u.AllWorkspaces, u.TargetRef)
	if err != nil {
		return "", domain.Config{}, nil, err
	}

	return configPath, config, workspaces, nil
}

func (u EmergeTargets) emergeOnePasswordDocuments(configPath string, config domain.Config, statePath string, state *domain.State, workspaces []emergeWorkspace, now time.Time) error {
	if err := requireOnePasswordRuntime(u.DocumentRuntime); err != nil {
		return err
	}

	if u.AllWorkspaces {
		return u.emergeOnePasswordWorkspaces(configPath, config, statePath, state, workspaces, now)
	}

	originalState := cloneState(*state)
	targetChanges := []emergeTargetChange{}
	configChanged := false
	outputLayout := newEmergeOutputLayout(u.AllWorkspaces, workspaces)
	var emergeErr error

	for _, entry := range workspaces {
		ttl, err := config.EffectiveTTL(entry.workspace)
		if err != nil {
			if u.AllWorkspaces {
				wrappedErr := wrapEmergeWorkspaceError(u.AllWorkspaces, entry.id, err)
				outputLayout.writeWorkspaceFailure(u.Stdout, entry.id, wrappedErr)
				emergeErr = errors.Join(emergeErr, wrappedErr)
				continue
			}
			return wrappedErrOrRollback(u.FileSystem, statePath, originalState, targetChanges, wrapEmergeWorkspaceError(u.AllWorkspaces, entry.id, err))
		}
		if err := ensureWorkspaceRootExists(u.FileSystem, entry.workspace.Root); err != nil {
			if u.AllWorkspaces {
				wrappedErr := wrapEmergeWorkspaceError(u.AllWorkspaces, entry.id, err)
				outputLayout.writeWorkspaceFailure(u.Stdout, entry.id, wrappedErr)
				emergeErr = errors.Join(emergeErr, wrappedErr)
				continue
			}
			return wrappedErrOrRollback(u.FileSystem, statePath, originalState, targetChanges, wrapEmergeWorkspaceError(u.AllWorkspaces, entry.id, err))
		}

		for _, target := range entry.workspace.Targets {
			document, ok, err := config.DocumentForTarget(entry.id, target)
			if err == nil && !ok {
				err = fmt.Errorf("1Password document is not registered: %s", target)
			}
			if err != nil {
				wrappedErr := wrapEmergeTargetError(u.AllWorkspaces, entry.id, target, err)
				if u.AllWorkspaces {
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					emergeErr = errors.Join(emergeErr, wrappedErr)
					continue
				}
				return wrappedErrOrRollback(u.FileSystem, statePath, originalState, targetChanges, wrappedErr)
			}

			vault := onePasswordVault(config, document)
			workspaceTargetPath := filepath.Join(entry.workspace.Root, target)

			readStarted := time.Now()
			data, err := u.DocumentRuntime.ReadDocument(vault, document.ItemID)
			readDuration := time.Since(readStarted)
			writeEmergeVerbose(u.VerboseOutput, "1Password read: %s (%s)", emergeTargetLabel(u.AllWorkspaces, entry.id, target), readDuration.Round(time.Millisecond))
			if err != nil {
				wrappedErr := wrapEmergeTargetError(u.AllWorkspaces, entry.id, target, fmt.Errorf("read 1Password document: %w", err))
				if u.AllWorkspaces {
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					emergeErr = errors.Join(emergeErr, wrappedErr)
					continue
				}
				return wrappedErrOrRollback(u.FileSystem, statePath, originalState, targetChanges, wrappedErr)
			}

			materialized, err := ensureMaterializedFile(u.FileSystem, *state, entry.id, target, workspaceTargetPath, document.ItemID, data, now, u.OverwriteLocal)
			if err != nil {
				wrappedErr := wrapEmergeTargetError(u.AllWorkspaces, entry.id, target, err)
				if u.AllWorkspaces {
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					emergeErr = errors.Join(emergeErr, wrappedErr)
					continue
				}
				return wrappedErrOrRollback(u.FileSystem, statePath, originalState, targetChanges, wrappedErr)
			}
			if materialized.expiredModified {
				wrappedErr := wrapEmergeTargetError(u.AllWorkspaces, entry.id, target, expiredModifiedEmergeError(entry.id, target, u.AllWorkspaces, u.TargetRef))
				emergeErr = errors.Join(emergeErr, wrappedErr)
				continue
			}
			if materialized.created || materialized.overwritten {
				targetChanges = append(targetChanges, emergeTargetChange{
					path:         workspaceTargetPath,
					created:      materialized.created,
					previousData: materialized.previousData,
				})
			}

			hash := sha256Hex(data)
			if document.Vault != vault {
				document.Vault = vault
				if err := config.UpsertDocument(document); err != nil {
					return wrappedErrOrRollback(u.FileSystem, statePath, originalState, targetChanges, err)
				}
				configChanged = true
			}
			if err := updateLeaseHash(state, entry.id, target, workspaceTargetPath, document.ItemID, hash, now, ttl); err != nil {
				return wrappedErrOrRollback(u.FileSystem, statePath, originalState, targetChanges, err)
			}
			outputLayout.writeTarget(u.Stdout, entry.id, emergeTargetLabel(u.AllWorkspaces, entry.id, target), target, materialized.created)
		}
	}

	if err := persistState(u.FileSystem, statePath, *state); err != nil {
		return rollbackEmergeChanges(u.FileSystem, statePath, originalState, targetChanges, err)
	}
	if configChanged {
		configData, err := config.RenderTOML()
		if err != nil {
			return err
		}
		if err := u.FileSystem.WriteFile(configPath, configData, 0o644); err != nil {
			return fmt.Errorf("write config file: %w", err)
		}
	}
	if emergeErr != nil {
		return emergeErr
	}
	return nil
}

const emergeParallelism = 8

type emergeOnePasswordTask struct {
	order       int
	workspaceID string
	workspace   domain.Workspace
	target      string
	ttl         time.Duration
	document    domain.DocumentConfig
	vault       string
}

type emergeOnePasswordResult struct {
	order               int
	workspaceID         string
	target              string
	workspaceTargetPath string
	document            domain.DocumentConfig
	ttl                 time.Duration
	created             bool
	overwritten         bool
	previousData        []byte
	expiredModified     bool
	configChanged       bool
	readDuration        time.Duration
	err                 error
}

func (u EmergeTargets) emergeOnePasswordWorkspaces(configPath string, config domain.Config, statePath string, state *domain.State, workspaces []emergeWorkspace, now time.Time) error {
	originalState := cloneState(*state)
	outputLayout := newEmergeOutputLayout(true, workspaces)
	results := make([]emergeOnePasswordResult, 0)
	tasks := make([]emergeOnePasswordTask, 0)
	order := 0

	for _, entry := range workspaces {
		ttl, err := config.EffectiveTTL(entry.workspace)
		if err != nil {
			results = append(results, emergeOnePasswordResult{
				order:       order,
				workspaceID: entry.id,
				err:         wrapEmergeWorkspaceError(true, entry.id, err),
			})
			order++
			continue
		}
		if err := ensureWorkspaceRootExists(u.FileSystem, entry.workspace.Root); err != nil {
			results = append(results, emergeOnePasswordResult{
				order:       order,
				workspaceID: entry.id,
				err:         wrapEmergeWorkspaceError(true, entry.id, err),
			})
			order++
			continue
		}

		for _, target := range entry.workspace.Targets {
			document, ok, err := config.DocumentForTarget(entry.id, target)
			if err == nil && !ok {
				err = fmt.Errorf("1Password document is not registered: %s", target)
			}
			if err != nil {
				results = append(results, emergeOnePasswordResult{
					order:       order,
					workspaceID: entry.id,
					target:      target,
					err:         wrapEmergeTargetError(true, entry.id, target, err),
				})
				order++
				continue
			}

			vault := onePasswordVault(config, document)
			tasks = append(tasks, emergeOnePasswordTask{
				order:       order,
				workspaceID: entry.id,
				workspace:   entry.workspace,
				target:      target,
				ttl:         ttl,
				document:    document,
				vault:       vault,
			})
			order++
		}
	}

	if len(tasks) > 0 {
		authStarted := time.Now()
		if err := authenticateOnePasswordRuntime(u.DocumentRuntime); err != nil {
			return err
		}
		writeEmergeVerbose(u.VerboseOutput, "1Password authentication: %s", time.Since(authStarted).Round(time.Millisecond))
		writeEmergeVerbose(u.VerboseOutput, "1Password reads started: documents=%d parallelism=%d", len(tasks), min(len(tasks), emergeParallelism))
	}
	results = append(results, runEmergeOnePasswordTasks(u.FileSystem, u.DocumentRuntime, *state, now, tasks, func(result emergeOnePasswordResult) {
		writeEmergeVerbose(u.VerboseOutput, "1Password read completed: %s (%s)", emergeTargetLabel(true, result.workspaceID, result.target), result.readDuration.Round(time.Millisecond))
	})...)
	sort.Slice(results, func(i, j int) bool { return results[i].order < results[j].order })

	targetChanges := []emergeTargetChange{}
	configChanged := false
	var emergeErr error
	for _, result := range results {
		if result.err != nil {
			if result.expiredModified {
				outputLayout.writeTargetExpiredModified(u.Stdout, result.workspaceID, result.target)
				emergeErr = errors.Join(emergeErr, result.err)
				continue
			}
			if result.target == "" {
				outputLayout.writeWorkspaceFailure(u.Stdout, result.workspaceID, result.err)
			} else {
				outputLayout.writeTargetFailure(u.Stdout, result.workspaceID, result.target, result.err)
			}
			emergeErr = errors.Join(emergeErr, result.err)
			continue
		}

		if result.created || result.overwritten {
			targetChanges = append(targetChanges, emergeTargetChange{
				path:         result.workspaceTargetPath,
				created:      result.created,
				previousData: result.previousData,
			})
		}
		if result.configChanged {
			if err := config.UpsertDocument(result.document); err != nil {
				return err
			}
			configChanged = true
		}
		if err := updateLeaseHash(state, result.workspaceID, result.target, result.workspaceTargetPath, result.document.ItemID, result.document.ContentSHA256, now, result.ttl); err != nil {
			return wrappedErrOrRollback(u.FileSystem, statePath, originalState, targetChanges, err)
		}
		outputLayout.writeTarget(u.Stdout, result.workspaceID, emergeTargetLabel(true, result.workspaceID, result.target), result.target, result.created)
	}

	if err := persistState(u.FileSystem, statePath, *state); err != nil {
		return rollbackEmergeChanges(u.FileSystem, statePath, originalState, targetChanges, err)
	}
	if configChanged {
		if err := writeConfig(u.FileSystem, configPath, config); err != nil {
			return err
		}
	}
	return emergeErr
}

func runEmergeOnePasswordTasks(fs emergeFileSystem, runtime OnePasswordDocumentRuntime, state domain.State, now time.Time, tasks []emergeOnePasswordTask, onResult func(emergeOnePasswordResult)) []emergeOnePasswordResult {
	if len(tasks) == 0 {
		return nil
	}

	workerCount := emergeParallelism
	if len(tasks) < workerCount {
		workerCount = len(tasks)
	}

	taskCh := make(chan emergeOnePasswordTask, len(tasks))
	resultCh := make(chan emergeOnePasswordResult, len(tasks))
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskCh {
				resultCh <- runEmergeOnePasswordTask(fs, runtime, state, now, task)
			}
		}()
	}

	for _, task := range tasks {
		taskCh <- task
	}
	close(taskCh)
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	results := make([]emergeOnePasswordResult, 0, len(tasks))
	for result := range resultCh {
		if onResult != nil {
			onResult(result)
		}
		results = append(results, result)
	}
	return results
}

func runEmergeOnePasswordTask(fs emergeFileSystem, runtime OnePasswordDocumentRuntime, state domain.State, now time.Time, task emergeOnePasswordTask) emergeOnePasswordResult {
	result := emergeOnePasswordResult{
		order:       task.order,
		workspaceID: task.workspaceID,
		target:      task.target,
		document:    task.document,
		ttl:         task.ttl,
	}

	readStarted := time.Now()
	data, err := runtime.ReadDocument(task.vault, task.document.ItemID)
	result.readDuration = time.Since(readStarted)
	if err != nil {
		result.err = wrapEmergeTargetError(true, task.workspaceID, task.target, fmt.Errorf("read 1Password document: %w", err))
		return result
	}

	workspaceTargetPath := filepath.Join(task.workspace.Root, task.target)
	result.workspaceTargetPath = workspaceTargetPath
	materialized, err := ensureMaterializedFile(fs, state, task.workspaceID, task.target, workspaceTargetPath, task.document.ItemID, data, now, false)
	if err != nil {
		result.err = wrapEmergeTargetError(true, task.workspaceID, task.target, err)
		return result
	}
	if materialized.expiredModified {
		result.expiredModified = true
		result.err = wrapEmergeTargetError(true, task.workspaceID, task.target, expiredModifiedEmergeError(task.workspaceID, task.target, true, ""))
		return result
	}
	result.created = materialized.created
	result.overwritten = materialized.overwritten
	result.previousData = materialized.previousData

	hash := sha256Hex(data)
	result.document.ContentSHA256 = hash
	if result.document.Vault != task.vault {
		result.document.Vault = task.vault
		result.configChanged = true
	}
	return result
}

func writeEmergeVerbose(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, "verbose: "+format+"\n", args...)
}

func wrappedErrOrRollback(fs emergeFileSystem, statePath string, originalState domain.State, targetChanges []emergeTargetChange, err error) error {
	return rollbackEmergeChanges(fs, statePath, originalState, targetChanges, err)
}

type emergeOutputLayout struct {
	allWorkspaces  bool
	actionWidth    int
	workspaceWidth int
}

func newEmergeOutputLayout(allWorkspaces bool, workspaces []emergeWorkspace) emergeOutputLayout {
	layout := emergeOutputLayout{
		allWorkspaces: allWorkspaces,
		actionWidth:   len("expired-modified"),
	}
	if !allWorkspaces {
		return layout
	}

	for _, entry := range workspaces {
		if len(entry.id) > layout.workspaceWidth {
			layout.workspaceWidth = len(entry.id)
		}
	}

	return layout
}

func (l emergeOutputLayout) writeTarget(w io.Writer, workspaceID, targetLabel, target string, created bool) {
	action := "emerged"
	if !created {
		action = "already emerged"
	}

	if !l.allWorkspaces {
		fmt.Fprintf(w, "%s target: %s\n", action, targetLabel)
		return
	}

	fmt.Fprintf(w, "%-*s  repo: %-*s  file: %s\n", l.actionWidth, action, l.workspaceWidth, workspaceID, target)
}

func (l emergeOutputLayout) writeTargetExpiredModified(w io.Writer, workspaceID, target string) {
	targetRef := formatTargetRef(workspaceID, target)
	fmt.Fprintf(w, "%-*s  repo: %-*s  file: %s  note: local changes kept; inspect with veil diff %s\n", l.actionWidth, "expired-modified", l.workspaceWidth, workspaceID, target, targetRef)
}

func (l emergeOutputLayout) writeTargetFailure(w io.Writer, workspaceID, target string, err error) {
	if !l.allWorkspaces {
		fmt.Fprintf(w, "failed target: %s  error: %v\n", target, err)
		return
	}

	fmt.Fprintf(w, "%-*s  repo: %-*s  file: %s  error: %v\n", l.actionWidth, "failed", l.workspaceWidth, workspaceID, target, err)
}

func (l emergeOutputLayout) writeWorkspaceFailure(w io.Writer, workspaceID string, err error) {
	if !l.allWorkspaces {
		fmt.Fprintf(w, "failed workspace: %s  error: %v\n", workspaceID, err)
		return
	}

	fmt.Fprintf(w, "%-*s  repo: %-*s  error: %v\n", l.actionWidth, "failed", l.workspaceWidth, workspaceID, err)
}

func resolveEmergeWorkspaces(fs workspaceResolverFileSystem, config domain.Config, allWorkspaces bool, targetRef string) ([]emergeWorkspace, error) {
	if allWorkspaces && targetRef != "" {
		return nil, fmt.Errorf("--all and a target ref cannot be used together")
	}

	if allWorkspaces {
		ids := make([]string, 0, len(config.Workspaces))
		for id := range config.Workspaces {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		workspaces := make([]emergeWorkspace, 0, len(ids))
		for _, id := range ids {
			workspaces = append(workspaces, emergeWorkspace{
				id:        id,
				workspace: config.Workspaces[id],
			})
		}

		return workspaces, nil
	}
	if targetRef != "" {
		selection, err := resolveTargetSelection(fs, config, targetRef)
		if err != nil {
			return nil, err
		}
		selection.workspace.Targets = []string{selection.target}
		return []emergeWorkspace{{
			id:        selection.workspaceID,
			workspace: selection.workspace,
		}}, nil
	}

	workspaceID, workspace, err := currentWorkspace(config, fs)
	if err != nil {
		return nil, err
	}

	return []emergeWorkspace{{
		id:        workspaceID,
		workspace: workspace,
	}}, nil
}

func emergeTargetLabel(allWorkspaces bool, workspaceID, target string) string {
	if !allWorkspaces {
		return target
	}

	return workspaceID + ":" + target
}

func wrapEmergeWorkspaceError(allWorkspaces bool, workspaceID string, err error) error {
	if !allWorkspaces {
		return err
	}

	return fmt.Errorf("%s: %w", workspaceID, err)
}

func wrapEmergeTargetError(allWorkspaces bool, workspaceID, target string, err error) error {
	if !allWorkspaces {
		return err
	}

	return fmt.Errorf("%s: %w", emergeTargetLabel(allWorkspaces, workspaceID, target), err)
}

func expiredModifiedEmergeError(workspaceID, target string, allWorkspaces bool, requestedRef string) error {
	ref := requestedRef
	if ref == "" && allWorkspaces {
		ref = formatTargetRef(workspaceID, target)
	}
	if ref == "" {
		ref = target
	}
	if allWorkspaces {
		return fmt.Errorf("target is expired with local changes; run veil diff %s", ref)
	}
	return fmt.Errorf("target is expired with local changes: %s\n  inspect:      veil diff %s\n  keep changes: veil commit %s\n  use remote:   veil emerge --overwrite-local %s", ref, ref, ref, ref)
}

func ensureWorkspaceRootExists(fs emergeFileSystem, workspaceRoot string) error {
	info, err := fs.Stat(workspaceRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("workspace root does not exist: %s", workspaceRoot)
		}
		return fmt.Errorf("stat workspace root: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace root must be a directory: %s", workspaceRoot)
	}

	return nil
}

type emergeTargetChange struct {
	path         string
	created      bool
	previousData []byte
}

func rollbackEmergeChanges(fs emergeFileSystem, statePath string, originalState domain.State, targetChanges []emergeTargetChange, cause error) error {
	var rollbackErr error

	for i := len(targetChanges) - 1; i >= 0; i-- {
		change := targetChanges[i]
		if change.created {
			if err := fs.Remove(change.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback emerged target %s: %w", change.path, err))
			}
			continue
		}
		if err := fs.WriteFile(change.path, change.previousData, 0o600); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore emerged target %s: %w", change.path, err))
		}
	}

	if err := persistState(fs, statePath, originalState); err != nil {
		rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback state file: %w", err))
	}

	if rollbackErr != nil {
		return errors.Join(cause, rollbackErr)
	}

	return cause
}

func cloneState(state domain.State) domain.State {
	cloned := state
	cloned.Leases = append([]domain.Lease(nil), state.Leases...)
	return cloned
}
