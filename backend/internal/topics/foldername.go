package topics

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// ErrFolderNameInvalid rejects a folder name setting the client could not use
// as a folder name (issue #225). The wrapped message names the broken rule.
var ErrFolderNameInvalid = errors.New("invalid folder name")

// maxFolderNameBytes is the common file-name limit on Linux, Windows and
// macOS file systems.
const maxFolderNameBytes = 255

// windowsReserved are device names Windows refuses as a file name, with or
// without an extension. A client can run on Windows, so they are refused
// everywhere.
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// NormalizeFolderName validates a topic's folder name setting and returns the
// name to store: trimmed in custom mode, "" in every other mode (a name is
// stored only in custom mode). An empty mode is the default mode.
func NormalizeFolderName(mode domain.FolderNameMode, name string) (string, error) {
	switch mode {
	case "", domain.FolderNameDefault, domain.FolderNameKeep:
		return "", nil
	case domain.FolderNameCustom:
	default:
		return "", fmt.Errorf("%w: unknown mode %q", ErrFolderNameInvalid, mode)
	}
	name = strings.TrimSpace(name)
	if err := checkFolderName(name); err != nil {
		return "", fmt.Errorf("%w: %s", ErrFolderNameInvalid, err.Error())
	}
	return name, nil
}

func checkFolderName(name string) error {
	switch {
	case name == "":
		return errors.New("a custom folder name is required")
	case len(name) > maxFolderNameBytes:
		return fmt.Errorf("longer than %d bytes", maxFolderNameBytes)
	case name == "." || name == "..":
		return errors.New("not a folder name")
	case name == ".pad":
		// Clients hide BEP 47 padding under .pad; the scheduler would treat
		// the whole folder as padding.
		return errors.New(".pad is reserved for padding files")
	case strings.ContainsAny(name, `/\<>:"|?*`):
		return errors.New(`must not contain / \ < > : " | ? *`)
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return errors.New("must not contain control characters")
	case strings.HasSuffix(name, "."):
		return errors.New("must not end with a dot")
	}
	base, _, _ := strings.Cut(name, ".")
	if windowsReserved[strings.ToUpper(base)] {
		return fmt.Errorf("%s is a reserved name on Windows", base)
	}
	return nil
}
