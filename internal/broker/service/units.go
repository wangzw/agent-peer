package service

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	// Label is the launchd label shared by the per-user agent and the system
	// daemon.
	Label = "io.github.wangzw.agent-peer"
	// UnitName is the systemd unit name shared by the user and system units.
	UnitName = "agent-peer.service"
)

const systemdUserTemplate = `[Unit]
Description=agent-peer Broker for this account
Documentation=https://github.com/wangzw/agent-peer

[Service]
Type=simple
ExecStart=%s broker
Restart=always
RestartSec=5
UMask=0077

[Install]
WantedBy=default.target
`

const systemdSystemTemplate = `[Unit]
Description=agent-peer system Broker
Documentation=https://github.com/wangzw/agent-peer
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
ExecStart=%s broker --system
Restart=always
RestartSec=5
UMask=0022
# The Broker reaches every served account's session registry and inbox
# sockets through per-account helpers, so it must see the real /home, /tmp
# and /run. Do not add ProtectHome, PrivateTmp or ProtectSystem.

[Install]
WantedBy=multi-user.target
`

const launchAgentTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>io.github.wangzw.agent-peer</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>broker</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Background</string>
	<key>Umask</key>
	<integer>63</integer>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`

const launchDaemonTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>io.github.wangzw.agent-peer</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>broker</string>
		<string>--system</string>
	</array>
	<key>UserName</key>
	<string>root</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>Umask</key>
	<integer>18</integer>
	<key>StandardOutPath</key>
	<string>/var/log/agent-peer/broker.log</string>
	<key>StandardErrorPath</key>
	<string>/var/log/agent-peer/broker.log</string>
</dict>
</plist>
`

const systemConfigTemplate = `# agent-peer system Broker configuration.
#
# Every key is optional; a key that is absent takes its default value.
# Granting a group read access to this file lets its accounts read the
# configuration, the full status and the audit log; granting write access
# lets them change the configuration and manage pairing.
`

// SystemdUserUnit returns the systemd user unit that starts the Broker when
// the account logs in.
func SystemdUserUnit(exe string) (string, error) {
	exec, err := systemdExecStart(exe)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(systemdUserTemplate, exec), nil
}

// SystemdSystemUnit returns the systemd system unit that starts the system
// Broker when the machine boots.
func SystemdSystemUnit(exe string) (string, error) {
	exec, err := systemdExecStart(exe)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(systemdSystemTemplate, exec), nil
}

// LaunchAgentPlist returns the launchd agent that starts the Broker when the
// account logs in. Standard output and standard error go to logPath.
func LaunchAgentPlist(exe, logPath string) (string, error) {
	if err := validatePath("executable", exe); err != nil {
		return "", err
	}
	if err := validatePath("log path", logPath); err != nil {
		return "", err
	}
	escapedExe := escapeXML(exe)
	escapedLog := escapeXML(logPath)
	return fmt.Sprintf(launchAgentTemplate, escapedExe, escapedLog, escapedLog), nil
}

// LaunchDaemonPlist returns the launchd daemon that starts the system Broker
// when the machine boots.
func LaunchDaemonPlist(exe string) (string, error) {
	if err := validatePath("executable", exe); err != nil {
		return "", err
	}
	return fmt.Sprintf(launchDaemonTemplate, escapeXML(exe)), nil
}

// SystemConfigTemplate returns the commented-only initial content of the
// system configuration file.
func SystemConfigTemplate() string {
	return systemConfigTemplate
}

// validatePath rejects paths that cannot be embedded safely in a service
// definition: non-absolute paths and paths containing control characters.
func validatePath(kind, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("service: %s must be an absolute path, got %q", kind, path)
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("service: %s contains a control character", kind)
		}
	}
	return nil
}

// systemdExecStart renders path for an ExecStart= line. Percent signs are
// systemd specifiers and dollar signs start variables, so both are doubled.
// Paths containing whitespace or characters with meaning to the command line
// parser are quoted, with backslashes and double quotes escaped inside.
func systemdExecStart(path string) (string, error) {
	if err := validatePath("executable", path); err != nil {
		return "", err
	}
	exec := strings.ReplaceAll(path, "%", "%%")
	exec = strings.ReplaceAll(exec, "$", "$$")
	if strings.ContainsAny(path, "\"'\\;") || strings.ContainsFunc(path, unicode.IsSpace) {
		exec = strings.ReplaceAll(exec, `\`, `\\`)
		exec = strings.ReplaceAll(exec, `"`, `\"`)
		return `"` + exec + `"`, nil
	}
	return exec, nil
}

// escapeXML escapes the five characters that may not appear literally in XML
// character data or attribute values. A single pass avoids escaping the
// ampersands introduced by the other replacements.
func escapeXML(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	).Replace(s)
}
