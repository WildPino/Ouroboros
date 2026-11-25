//go:build windows

package windows

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// SystemPath returns the machine-wide PATH, with environment variables
// expanded: the search path of programs that run as SYSTEM, such as the
// scheduled task. It does not include the PATH of the current user.
func SystemPath() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, registry.QUERY_VALUE)
	if err != nil {
		return "", fmt.Errorf("open system environment: %w", err)
	}
	defer k.Close()
	v, typ, err := k.GetStringValue("Path")
	if err != nil {
		return "", fmt.Errorf("read system PATH: %w", err)
	}
	if typ == registry.EXPAND_SZ {
		if v, err = registry.ExpandString(v); err != nil {
			return "", fmt.Errorf("expand system PATH: %w", err)
		}
	}
	return v, nil
}
