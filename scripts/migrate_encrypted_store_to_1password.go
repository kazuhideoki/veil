//go:build ignore

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kazuhideoki/veil/internal/domain"
)

type inspectOutput struct {
	Backend    string `json:"backend"`
	BundlePath string `json:"bundle_path"`
	MountPath  string `json:"mount_path"`
	KeyRef     string `json:"key_ref"`
}

func main() {
	if len(os.Args) < 2 {
		fatalf("missing subcommand")
	}

	switch os.Args[1] {
	case "inspect":
		runInspect(os.Args[2:])
	case "migrate":
		runMigrate(os.Args[2:])
	default:
		fatalf("unknown subcommand: %s", os.Args[1])
	}
}

func runInspect(args []string) {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	configPath := fs.String("config-path", "", "Veil config path")
	_ = fs.Parse(args)
	if *configPath == "" {
		fatalf("--config-path is required")
	}

	config := loadConfig(*configPath)
	home := mustHomeDir()
	config = expandConfigPaths(config, home)
	output := inspectOutput{
		Backend:    config.Store.Backend,
		BundlePath: config.Store.BundlePath,
		MountPath:  config.Store.MountPath,
		KeyRef:     config.KeyProvider.Ref,
	}
	data, err := json.Marshal(output)
	if err != nil {
		fatalf("marshal inspect output: %v", err)
	}
	fmt.Println(string(data))
}

func runMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	configPath := fs.String("config-path", "", "Veil config path")
	storeRoot := fs.String("store-root", "", "mounted encrypted store root")
	vault := fs.String("vault", "Personal", "destination 1Password vault")
	dryRun := fs.Bool("dry-run", false, "print plan without creating items")
	_ = fs.Parse(args)
	if *configPath == "" || *storeRoot == "" {
		fatalf("--config-path and --store-root are required")
	}

	config := loadConfig(*configPath)
	if config.Store.Backend != domain.EncryptedVolumeBackend {
		fatalf("config backend must be %q, got %q", domain.EncryptedVolumeBackend, config.Store.Backend)
	}
	home := mustHomeDir()
	config = expandConfigPaths(config, home)

	targets := migrationTargets(config)
	if len(targets) == 0 {
		fatalf("config has no registered targets to migrate")
	}

	fmt.Printf("migrating %d target(s) to 1Password vault %q\n", len(targets), *vault)
	migrated := make([]domain.DocumentConfig, 0, len(targets))
	for _, target := range targets {
		storePath := filepath.Join(*storeRoot, "workspaces", target.workspaceID, filepath.FromSlash(target.target))
		data, err := os.ReadFile(storePath)
		if err != nil {
			fatalf("read store target %s/%s: %v", target.workspaceID, target.target, err)
		}
		title := onePasswordTitle(target.workspaceID, target.target)
		hash := sha256Hex(data)
		if *dryRun {
			fmt.Printf("would create document: title=%q target=%s/%s bytes=%d sha256=%s\n", title, target.workspaceID, target.target, len(data), hash)
			migrated = append(migrated, domain.DocumentConfig{
				WorkspaceID:   target.workspaceID,
				Target:        target.target,
				ItemID:        "dry-run",
				Vault:         *vault,
				Title:         title,
				ContentSHA256: hash,
			})
			continue
		}

		itemID, reused, err := createOrReuseDocument(*vault, title, onePasswordTags(target.workspaceID), data)
		if err != nil {
			fatalf("create 1Password document for %s/%s: %v", target.workspaceID, target.target, err)
		}
		if reused {
			fmt.Printf("reused document: target=%s/%s item_id=%s\n", target.workspaceID, target.target, itemID)
		} else {
			fmt.Printf("created document: target=%s/%s item_id=%s\n", target.workspaceID, target.target, itemID)
		}
		migrated = append(migrated, domain.DocumentConfig{
			WorkspaceID:   target.workspaceID,
			Target:        target.target,
			ItemID:        itemID,
			Vault:         *vault,
			Title:         title,
			ContentSHA256: hash,
		})
	}

	if *dryRun {
		fmt.Println("dry run complete; config was not changed")
		return
	}

	nextConfig := config
	nextConfig.Store.Backend = domain.OnePasswordBackend
	nextConfig.Store.BundlePath = ""
	nextConfig.Store.MountPath = ""
	nextConfig.Store.VolumeName = ""
	nextConfig.Store.Vault = *vault
	nextConfig.KeyProvider = domain.KeyProviderConfig{}
	nextConfig.Session = domain.SessionConfig{}
	nextConfig.Documents = nil
	for _, document := range migrated {
		if err := nextConfig.UpsertDocument(document); err != nil {
			fatalf("record document metadata: %v", err)
		}
	}

	backupPath := *configPath + ".bak"
	if err := copyFile(*configPath, backupPath); err != nil {
		fatalf("backup config: %v", err)
	}
	rendered, err := nextConfig.RenderTOML()
	if err != nil {
		fatalf("render migrated config: %v", err)
	}
	if err := os.WriteFile(*configPath, rendered, 0o644); err != nil {
		fatalf("write migrated config: %v", err)
	}
	fmt.Printf("backup config: %s\n", backupPath)
	fmt.Printf("wrote migrated config: %s\n", *configPath)
}

type migrationTarget struct {
	workspaceID string
	target      string
}

func migrationTargets(config domain.Config) []migrationTarget {
	workspaceIDs := make([]string, 0, len(config.Workspaces))
	for id := range config.Workspaces {
		workspaceIDs = append(workspaceIDs, id)
	}
	sort.Strings(workspaceIDs)

	targets := []migrationTarget{}
	for _, id := range workspaceIDs {
		workspace := config.Workspaces[id]
		workspaceTargets := append([]string(nil), workspace.Targets...)
		sort.Strings(workspaceTargets)
		for _, target := range workspaceTargets {
			targets = append(targets, migrationTarget{workspaceID: id, target: target})
		}
	}
	return targets
}

func loadConfig(path string) domain.Config {
	data, err := os.ReadFile(path)
	if err != nil {
		fatalf("read config: %v", err)
	}
	config, err := domain.ParseConfigTOML(data)
	if err != nil {
		fatalf("parse config: %v", err)
	}
	return config
}

func expandConfigPaths(config domain.Config, home string) domain.Config {
	config.StorePath = expandHomeDir(config.StorePath, home)
	config.Store.BundlePath = expandHomeDir(config.Store.BundlePath, home)
	config.Store.MountPath = expandHomeDir(config.Store.MountPath, home)
	config.Session.Directory = expandHomeDir(config.Session.Directory, home)
	return config
}

func expandHomeDir(path, home string) string {
	if path == "~" {
		return home
	}
	prefix := "~" + string(filepath.Separator)
	if strings.HasPrefix(path, prefix) {
		return filepath.Join(home, path[len(prefix):])
	}
	return path
}

func mustHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fatalf("resolve home directory: %v", err)
	}
	return home
}

func onePasswordTitle(workspaceID, target string) string {
	return fmt.Sprintf("Veil: %s: %s", workspaceID, target)
}

func onePasswordTags(workspaceID string) []string {
	return []string{"veil", "veil/workspace/" + workspaceID}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func createOrReuseDocument(vault, title string, tags []string, data []byte) (string, bool, error) {
	if itemID, ok, err := existingDocumentID(vault, title, data); err != nil {
		return "", false, err
	} else if ok {
		return itemID, true, nil
	}

	itemID, err := createDocument(vault, title, tags, data)
	if err != nil {
		return "", false, err
	}
	return itemID, false, nil
}

func createDocument(vault, title string, tags []string, data []byte) (string, error) {
	tempFile, err := os.CreateTemp("", "veil-migrate-1password-*")
	if err != nil {
		return "", err
	}
	tempPath := tempFile.Name()
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return "", err
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", err
	}
	defer func() {
		_ = os.Remove(tempPath)
	}()

	args := []string{"document", "create", tempPath, "--vault", vault, "--title", title, "--format", "json"}
	if len(tags) > 0 {
		args = append(args, "--tags", strings.Join(tags, ","))
	}
	output, err := exec.Command("op", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("op document create failed: %s", strings.TrimSpace(string(output)))
	}
	itemID, err := itemIDFromJSON(output)
	if err != nil {
		return "", err
	}
	return itemID, nil
}

func existingDocumentID(vault, title string, data []byte) (string, bool, error) {
	output, err := exec.Command("op", "item", "get", title, "--vault", vault, "--format", "json").CombinedOutput()
	if err != nil {
		return "", false, nil
	}
	itemID, err := itemIDFromJSON(output)
	if err != nil {
		return "", false, fmt.Errorf("parse existing item response for %q: %w", title, err)
	}
	existingData, err := readDocument(vault, itemID)
	if err != nil {
		return "", false, fmt.Errorf("verify existing document %q: %w", title, err)
	}
	if sha256Hex(existingData) != sha256Hex(data) {
		return "", false, fmt.Errorf("existing document %q has different content; delete it or rename it before rerunning", title)
	}
	return itemID, true, nil
}

func readDocument(vault, itemID string) ([]byte, error) {
	tempFile, err := os.CreateTemp("", "veil-migrate-1password-read-*")
	if err != nil {
		return nil, err
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return nil, err
	}
	defer func() {
		_ = os.Remove(tempPath)
	}()
	if err := os.Remove(tempPath); err != nil {
		return nil, err
	}

	output, err := exec.Command("op", "document", "get", itemID, "--vault", vault, "--out-file", tempPath).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("op document get failed: %s", strings.TrimSpace(string(output)))
	}
	return os.ReadFile(tempPath)
}

func itemIDFromJSON(data []byte) (string, error) {
	var item struct {
		ID     string `json:"id"`
		UUID   string `json:"uuid"`
		ItemID string `json:"item_id"`
	}
	if err := json.Unmarshal(data, &item); err != nil {
		return "", fmt.Errorf("parse op response: %w", err)
	}
	switch {
	case item.ID != "":
		return item.ID, nil
	case item.UUID != "":
		return item.UUID, nil
	case item.ItemID != "":
		return item.ItemID, nil
	default:
		return "", fmt.Errorf("op response did not include item id")
	}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
