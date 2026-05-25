package usecase

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const DefaultTTLAgentLabel = "com.kazuhideoki.veil.ttl-cleaner"
const defaultTTLAgentIntervalSeconds = 60

type ttlAgentFileSystem interface {
	UserHomeDir() (string, error)
	MkdirAll(path string, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	Stat(name string) (os.FileInfo, error)
	Remove(name string) error
}

type CommandRunner interface {
	Run(command string, args ...string) ([]byte, error)
}

type TTLAgent struct {
	FileSystem      ttlAgentFileSystem
	CommandRunner   CommandRunner
	Stdout          io.Writer
	ExecutablePath  string
	UserID          string
	Label           string
	IntervalSeconds int
}

func (u TTLAgent) Install() error {
	settings, err := u.settings()
	if err != nil {
		return err
	}
	if err := u.validateExecutable(); err != nil {
		return err
	}

	if err := u.FileSystem.MkdirAll(filepath.Dir(settings.plistPath), 0o755); err != nil {
		return fmt.Errorf("create launch agent directory: %w", err)
	}
	if err := u.FileSystem.MkdirAll(filepath.Dir(settings.stdoutPath), 0o755); err != nil {
		return fmt.Errorf("create veil directory: %w", err)
	}

	plistData := u.renderPlist(settings)
	if err := u.FileSystem.WriteFile(settings.plistPath, plistData, 0o644); err != nil {
		return fmt.Errorf("write launch agent plist: %w", err)
	}

	if u.CommandRunner != nil {
		_ = u.runLaunchctl("bootout", settings.serviceTarget)
		if err := u.runLaunchctl("bootstrap", settings.domainTarget, settings.plistPath); err != nil {
			return err
		}
		if err := u.runLaunchctl("kickstart", "-k", settings.serviceTarget); err != nil {
			return err
		}
	}

	if u.Stdout != nil {
		fmt.Fprintf(u.Stdout, "installed ttl agent: %s\n", settings.label)
		fmt.Fprintf(u.Stdout, "interval seconds: %d\n", settings.intervalSeconds)
		fmt.Fprintf(u.Stdout, "plist: %s\n", settings.plistPath)
	}
	return nil
}

func (u TTLAgent) EnsureInstalled() error {
	settings, err := u.settings()
	if err != nil {
		return err
	}
	if err := u.validateExecutable(); err != nil {
		return err
	}

	matches, err := u.plistMatches(settings)
	if err != nil {
		return err
	}
	if !matches {
		return u.Install()
	}
	if u.CommandRunner != nil {
		if _, err := u.CommandRunner.Run("launchctl", "print", settings.serviceTarget); err == nil {
			return nil
		}
	} else {
		return nil
	}

	return u.Install()
}

func (u TTLAgent) Uninstall() error {
	settings, err := u.settings()
	if err != nil {
		return err
	}

	if u.CommandRunner != nil {
		_ = u.runLaunchctl("bootout", settings.serviceTarget)
	}
	if err := u.FileSystem.Remove(settings.plistPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove launch agent plist: %w", err)
	}

	if u.Stdout != nil {
		fmt.Fprintf(u.Stdout, "uninstalled ttl agent: %s\n", settings.label)
	}
	return nil
}

func (u TTLAgent) Status() error {
	settings, err := u.settings()
	if err != nil {
		return err
	}

	installed := true
	if _, err := u.FileSystem.Stat(settings.plistPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat launch agent plist: %w", err)
		}
		installed = false
	}

	loaded := false
	if u.CommandRunner != nil {
		if _, err := u.CommandRunner.Run("launchctl", "print", settings.serviceTarget); err == nil {
			loaded = true
		}
	}

	if u.Stdout != nil {
		fmt.Fprintf(u.Stdout, "ttl agent: %s\n", settings.label)
		fmt.Fprintf(u.Stdout, "installed: %s\n", yesNo(installed))
		fmt.Fprintf(u.Stdout, "loaded: %s\n", yesNo(loaded))
		fmt.Fprintf(u.Stdout, "plist: %s\n", settings.plistPath)
	}
	return nil
}

func (u TTLAgent) runLaunchctl(args ...string) error {
	output, err := u.CommandRunner.Run("launchctl", args...)
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if message == "" {
		message = err.Error()
	}
	return fmt.Errorf("launchctl %s: %s", strings.Join(args, " "), message)
}

func (u TTLAgent) validateExecutable() error {
	if u.ExecutablePath == "" {
		return fmt.Errorf("ttl-agent install requires an executable path")
	}
	if !filepath.IsAbs(u.ExecutablePath) {
		return fmt.Errorf("ttl-agent executable path must be absolute: %s", u.ExecutablePath)
	}
	info, err := u.FileSystem.Stat(u.ExecutablePath)
	if err != nil {
		return fmt.Errorf("stat ttl-agent executable: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("ttl-agent executable path must be a file: %s", u.ExecutablePath)
	}
	return nil
}

func (u TTLAgent) plistMatches(settings ttlAgentSettings) (bool, error) {
	currentData, err := u.FileSystem.ReadFile(settings.plistPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read launch agent plist: %w", err)
	}
	return bytes.Equal(currentData, u.renderPlist(settings)), nil
}

func (u TTLAgent) renderPlist(settings ttlAgentSettings) []byte {
	return renderTTLAgentPlist(ttlAgentPlist{
		Label:           settings.label,
		ExecutablePath:  u.ExecutablePath,
		HomeDir:         settings.homeDir,
		IntervalSeconds: settings.intervalSeconds,
		StdoutPath:      settings.stdoutPath,
		StderrPath:      settings.stderrPath,
	})
}

type ttlAgentSettings struct {
	homeDir         string
	label           string
	intervalSeconds int
	plistPath       string
	stdoutPath      string
	stderrPath      string
	domainTarget    string
	serviceTarget   string
}

func (u TTLAgent) settings() (ttlAgentSettings, error) {
	if u.FileSystem == nil {
		return ttlAgentSettings{}, fmt.Errorf("ttl-agent requires a file system")
	}
	homeDir, err := u.FileSystem.UserHomeDir()
	if err != nil {
		return ttlAgentSettings{}, fmt.Errorf("resolve home directory: %w", err)
	}
	label := u.Label
	if label == "" {
		label = DefaultTTLAgentLabel
	}
	if strings.ContainsAny(label, "/:") {
		return ttlAgentSettings{}, fmt.Errorf("ttl-agent label must not contain '/' or ':'")
	}
	intervalSeconds := u.IntervalSeconds
	if intervalSeconds == 0 {
		intervalSeconds = defaultTTLAgentIntervalSeconds
	}
	if intervalSeconds <= 0 {
		return ttlAgentSettings{}, fmt.Errorf("ttl-agent interval must be greater than zero")
	}
	if u.UserID == "" {
		return ttlAgentSettings{}, fmt.Errorf("ttl-agent requires a user id")
	}

	domainTarget := "gui/" + u.UserID
	return ttlAgentSettings{
		homeDir:         homeDir,
		label:           label,
		intervalSeconds: intervalSeconds,
		plistPath:       filepath.Join(homeDir, "Library", "LaunchAgents", label+".plist"),
		stdoutPath:      filepath.Join(homeDir, ".veil", "ttl-cleaner.log"),
		stderrPath:      filepath.Join(homeDir, ".veil", "ttl-cleaner.err.log"),
		domainTarget:    domainTarget,
		serviceTarget:   domainTarget + "/" + label,
	}, nil
}

type ttlAgentPlist struct {
	Label           string
	ExecutablePath  string
	HomeDir         string
	IntervalSeconds int
	StdoutPath      string
	StderrPath      string
}

func renderTTLAgentPlist(plist ttlAgentPlist) []byte {
	var builder strings.Builder
	builder.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	builder.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	builder.WriteString("<plist version=\"1.0\">\n")
	builder.WriteString("<dict>\n")
	writePlistString(&builder, "Label", plist.Label)
	builder.WriteString("\t<key>ProgramArguments</key>\n")
	builder.WriteString("\t<array>\n")
	writePlistArrayString(&builder, plist.ExecutablePath)
	writePlistArrayString(&builder, "ttl-cleaner")
	builder.WriteString("\t</array>\n")
	builder.WriteString("\t<key>EnvironmentVariables</key>\n")
	builder.WriteString("\t<dict>\n")
	writePlistString(&builder, "HOME", plist.HomeDir)
	builder.WriteString("\t</dict>\n")
	writePlistInteger(&builder, "StartInterval", plist.IntervalSeconds)
	builder.WriteString("\t<key>RunAtLoad</key>\n")
	builder.WriteString("\t<true/>\n")
	writePlistString(&builder, "StandardOutPath", plist.StdoutPath)
	writePlistString(&builder, "StandardErrorPath", plist.StderrPath)
	builder.WriteString("</dict>\n")
	builder.WriteString("</plist>\n")
	return []byte(builder.String())
}

func writePlistString(builder *strings.Builder, key, value string) {
	fmt.Fprintf(builder, "\t<key>%s</key>\n", escapeXML(key))
	fmt.Fprintf(builder, "\t<string>%s</string>\n", escapeXML(value))
}

func writePlistInteger(builder *strings.Builder, key string, value int) {
	fmt.Fprintf(builder, "\t<key>%s</key>\n", escapeXML(key))
	fmt.Fprintf(builder, "\t<integer>%d</integer>\n", value)
}

func writePlistArrayString(builder *strings.Builder, value string) {
	fmt.Fprintf(builder, "\t\t<string>%s</string>\n", escapeXML(value))
}

func escapeXML(value string) string {
	var buffer bytes.Buffer
	_ = xml.EscapeText(&buffer, []byte(value))
	return buffer.String()
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
