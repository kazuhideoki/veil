package infra

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type OnePasswordDocumentRuntime struct{}

func (OnePasswordDocumentRuntime) Authenticate() error {
	output, err := exec.Command("op", "account", "get").CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

func (OnePasswordDocumentRuntime) CreateDocument(vault, title string, tags []string, data []byte) (string, error) {
	tempFile, err := os.CreateTemp("", "veil-1password-create-*")
	if err != nil {
		return "", fmt.Errorf("create temporary document: %w", err)
	}
	tempPath := tempFile.Name()
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("write temporary document: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("close temporary document: %w", err)
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
		return "", fmt.Errorf("failed to create 1Password document: %s", strings.TrimSpace(string(output)))
	}
	itemID, err := itemIDFromJSON(output)
	if err != nil {
		return "", err
	}
	return itemID, nil
}

func (OnePasswordDocumentRuntime) ReadDocument(vault, itemID string) ([]byte, error) {
	tempFile, err := os.CreateTemp("", "veil-1password-read-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary document: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("close temporary document: %w", err)
	}
	defer func() {
		_ = os.Remove(tempPath)
	}()
	if err := os.Remove(tempPath); err != nil {
		return nil, fmt.Errorf("prepare temporary document path: %w", err)
	}

	cmd := exec.Command("op", "document", "get", itemID, "--vault", vault, "--out-file", tempPath)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to read 1Password document")
	}
	data, err := os.ReadFile(tempPath)
	if err != nil {
		return nil, fmt.Errorf("read temporary document: %w", err)
	}
	return data, nil
}

func (OnePasswordDocumentRuntime) UpdateDocument(vault, itemID string, data []byte) error {
	tempFile, err := os.CreateTemp("", "veil-1password-update-*")
	if err != nil {
		return fmt.Errorf("create temporary document: %w", err)
	}
	tempPath := tempFile.Name()
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("write temporary document: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close temporary document: %w", err)
	}
	defer func() {
		_ = os.Remove(tempPath)
	}()

	cmd := exec.Command("op", "document", "edit", itemID, tempPath, "--vault", vault)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to update 1Password document")
	}
	return nil
}

func itemIDFromJSON(data []byte) (string, error) {
	var item struct {
		ID     string `json:"id"`
		UUID   string `json:"uuid"`
		ItemID string `json:"item_id"`
	}
	if err := json.Unmarshal(data, &item); err != nil {
		return "", fmt.Errorf("parse 1Password response: %w", err)
	}
	switch {
	case item.ID != "":
		return item.ID, nil
	case item.UUID != "":
		return item.UUID, nil
	case item.ItemID != "":
		return item.ItemID, nil
	default:
		return "", fmt.Errorf("1Password response did not include item id")
	}
}
