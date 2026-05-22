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
	Now             func() time.Time
	AllWorkspaces   bool
}

type emergeWorkspace struct {
	id        string
	workspace domain.Workspace
}

func (u EmergeTargets) Run() error {
	configPath, config, err := loadConfig(u.FileSystem)
	if err != nil {
		return err
	}

	homeDir, err := u.FileSystem.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}

	config = expandConfigPaths(config, homeDir)
	config = canonicalizeWorkspaceRoots(config, u.FileSystem)

	statePath, state, err := loadState(u.FileSystem)
	if err != nil {
		return err
	}

	now := currentTime(u.Now)
	if err := requireOnePasswordConfig(config); err != nil {
		return err
	}

	workspaces, err := resolveEmergeWorkspaces(u.FileSystem, config, u.AllWorkspaces)
	if err != nil {
		return err
	}

	return u.emergeOnePasswordDocuments(configPath, config, statePath, &state, workspaces, now)
}

func (u EmergeTargets) emergeOnePasswordDocuments(configPath string, config domain.Config, statePath string, state *domain.State, workspaces []emergeWorkspace, now time.Time) error {
	if err := requireOnePasswordRuntime(u.DocumentRuntime); err != nil {
		return err
	}

	if u.AllWorkspaces {
		return u.emergeOnePasswordWorkspaces(configPath, config, statePath, state, workspaces, now)
	}

	originalState := cloneState(*state)
	createdTargetPaths := []string{}
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
			return wrappedErrOrRollback(u.FileSystem, statePath, originalState, createdTargetPaths, wrapEmergeWorkspaceError(u.AllWorkspaces, entry.id, err))
		}
		if err := ensureWorkspaceRootExists(u.FileSystem, entry.workspace.Root); err != nil {
			if u.AllWorkspaces {
				wrappedErr := wrapEmergeWorkspaceError(u.AllWorkspaces, entry.id, err)
				outputLayout.writeWorkspaceFailure(u.Stdout, entry.id, wrappedErr)
				emergeErr = errors.Join(emergeErr, wrappedErr)
				continue
			}
			return wrappedErrOrRollback(u.FileSystem, statePath, originalState, createdTargetPaths, wrapEmergeWorkspaceError(u.AllWorkspaces, entry.id, err))
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
				return wrappedErrOrRollback(u.FileSystem, statePath, originalState, createdTargetPaths, wrappedErr)
			}

			vault := onePasswordVault(config, document)
			data, err := u.DocumentRuntime.ReadDocument(vault, document.ItemID)
			if err != nil {
				wrappedErr := wrapEmergeTargetError(u.AllWorkspaces, entry.id, target, fmt.Errorf("read 1Password document: %w", err))
				if u.AllWorkspaces {
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					emergeErr = errors.Join(emergeErr, wrappedErr)
					continue
				}
				return wrappedErrOrRollback(u.FileSystem, statePath, originalState, createdTargetPaths, wrappedErr)
			}

			workspaceTargetPath := filepath.Join(entry.workspace.Root, target)
			created, err := ensureMaterializedFile(u.FileSystem, *state, entry.id, target, workspaceTargetPath, document.ItemID, data, now)
			if err != nil {
				wrappedErr := wrapEmergeTargetError(u.AllWorkspaces, entry.id, target, err)
				if u.AllWorkspaces {
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					emergeErr = errors.Join(emergeErr, wrappedErr)
					continue
				}
				return wrappedErrOrRollback(u.FileSystem, statePath, originalState, createdTargetPaths, wrappedErr)
			}
			if created {
				createdTargetPaths = append(createdTargetPaths, workspaceTargetPath)
			}

			hash := sha256Hex(data)
			if document.ContentSHA256 != hash || document.Vault != vault {
				document.ContentSHA256 = hash
				document.Vault = vault
				if err := config.UpsertDocument(document); err != nil {
					return err
				}
				configChanged = true
			}
			if err := updateLeaseHash(state, entry.id, target, workspaceTargetPath, document.ItemID, hash, now, ttl); err != nil {
				return wrappedErrOrRollback(u.FileSystem, statePath, originalState, createdTargetPaths, err)
			}
			outputLayout.writeTarget(u.Stdout, entry.id, emergeTargetLabel(u.AllWorkspaces, entry.id, target), target, created)
		}
	}

	if err := persistState(u.FileSystem, statePath, *state); err != nil {
		return rollbackEmergeChanges(u.FileSystem, statePath, originalState, createdTargetPaths, err)
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

const emergeParallelism = 4

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
	configChanged       bool
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

			tasks = append(tasks, emergeOnePasswordTask{
				order:       order,
				workspaceID: entry.id,
				workspace:   entry.workspace,
				target:      target,
				ttl:         ttl,
				document:    document,
				vault:       onePasswordVault(config, document),
			})
			order++
		}
	}

	if len(tasks) > 0 {
		if err := authenticateOnePasswordRuntime(u.DocumentRuntime); err != nil {
			return err
		}
	}
	results = append(results, runEmergeOnePasswordTasks(u.FileSystem, u.DocumentRuntime, *state, now, tasks)...)
	sort.Slice(results, func(i, j int) bool { return results[i].order < results[j].order })

	createdTargetPaths := []string{}
	configChanged := false
	var emergeErr error
	for _, result := range results {
		if result.err != nil {
			if result.target == "" {
				outputLayout.writeWorkspaceFailure(u.Stdout, result.workspaceID, result.err)
			} else {
				outputLayout.writeTargetFailure(u.Stdout, result.workspaceID, result.target, result.err)
			}
			emergeErr = errors.Join(emergeErr, result.err)
			continue
		}

		if result.created {
			createdTargetPaths = append(createdTargetPaths, result.workspaceTargetPath)
		}
		if result.configChanged {
			if err := config.UpsertDocument(result.document); err != nil {
				return err
			}
			configChanged = true
		}
		if err := updateLeaseHash(state, result.workspaceID, result.target, result.workspaceTargetPath, result.document.ItemID, result.document.ContentSHA256, now, result.ttl); err != nil {
			return wrappedErrOrRollback(u.FileSystem, statePath, originalState, createdTargetPaths, err)
		}
		outputLayout.writeTarget(u.Stdout, result.workspaceID, emergeTargetLabel(true, result.workspaceID, result.target), result.target, result.created)
	}

	if err := persistState(u.FileSystem, statePath, *state); err != nil {
		return rollbackEmergeChanges(u.FileSystem, statePath, originalState, createdTargetPaths, err)
	}
	if configChanged {
		if err := writeConfig(u.FileSystem, configPath, config); err != nil {
			return err
		}
	}
	return emergeErr
}

func runEmergeOnePasswordTasks(fs emergeFileSystem, runtime OnePasswordDocumentRuntime, state domain.State, now time.Time, tasks []emergeOnePasswordTask) []emergeOnePasswordResult {
	if len(tasks) == 0 {
		return nil
	}

	workerCount := emergeParallelism
	if len(tasks) < workerCount {
		workerCount = len(tasks)
	}

	taskCh := make(chan emergeOnePasswordTask)
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
	wg.Wait()
	close(resultCh)

	results := make([]emergeOnePasswordResult, 0, len(tasks))
	for result := range resultCh {
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

	data, err := runtime.ReadDocument(task.vault, task.document.ItemID)
	if err != nil {
		result.err = wrapEmergeTargetError(true, task.workspaceID, task.target, fmt.Errorf("read 1Password document: %w", err))
		return result
	}

	workspaceTargetPath := filepath.Join(task.workspace.Root, task.target)
	result.workspaceTargetPath = workspaceTargetPath
	created, err := ensureMaterializedFile(fs, state, task.workspaceID, task.target, workspaceTargetPath, task.document.ItemID, data, now)
	if err != nil {
		result.err = wrapEmergeTargetError(true, task.workspaceID, task.target, err)
		return result
	}
	result.created = created

	hash := sha256Hex(data)
	if result.document.ContentSHA256 != hash || result.document.Vault != task.vault {
		result.document.ContentSHA256 = hash
		result.document.Vault = task.vault
		result.configChanged = true
	}
	return result
}

func wrappedErrOrRollback(fs emergeFileSystem, statePath string, originalState domain.State, createdTargetPaths []string, err error) error {
	return rollbackEmergeChanges(fs, statePath, originalState, createdTargetPaths, err)
}

type emergeOutputLayout struct {
	allWorkspaces  bool
	actionWidth    int
	workspaceWidth int
}

func newEmergeOutputLayout(allWorkspaces bool, workspaces []emergeWorkspace) emergeOutputLayout {
	layout := emergeOutputLayout{
		allWorkspaces: allWorkspaces,
		actionWidth:   len("already emerged"),
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

func resolveEmergeWorkspaces(fs workspaceResolverFileSystem, config domain.Config, allWorkspaces bool) ([]emergeWorkspace, error) {
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

	currentDir, err := fs.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve current directory: %w", err)
	}

	currentDir, err = fs.EvalSymlinks(currentDir)
	if err != nil {
		return nil, fmt.Errorf("canonicalize current directory: %w", err)
	}

	workspaceID, workspace, err := config.ResolveWorkspaceByDir(currentDir)
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

func rollbackEmergeChanges(fs emergeFileSystem, statePath string, originalState domain.State, createdTargetPaths []string, cause error) error {
	var rollbackErr error

	for i := len(createdTargetPaths) - 1; i >= 0; i-- {
		if err := fs.Remove(createdTargetPaths[i]); err != nil && !errors.Is(err, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback emerged target %s: %w", createdTargetPaths[i], err))
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
