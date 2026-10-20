package expand

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/google/shlex"

	"gitlab.com/gitlab-org/cli/internal/config"
)

// ExpandAlias processes argv to see if it should be rewritten according to a user's aliases. The
// second return value indicates whether the alias should be executed in a new shell process instead
// of running gh itself.
func ExpandAlias(cfg config.Config, args []string, findShFunc func() (string, error)) ([]string, bool, error) {
	if len(args) < 2 {
		// the command is lacking a subcommand
		return nil, false, nil
	}
	expanded := args[1:]

	aliases, err := cfg.Aliases()
	if err != nil {
		return expanded, false, err
	}

	expansion, ok := aliases.Get(args[1])
	if !ok {
		return expanded, false, nil
	}

	if strings.HasPrefix(expansion, "!") {
		isShell := true
		if findShFunc == nil {
			findShFunc = findSh
		}
		shPath, shErr := findShFunc()
		if shErr != nil {
			return nil, false, shErr
		}

		expanded = []string{shPath, "-c", expansion[1:]}

		if len(args[2:]) > 0 {
			expanded = append(expanded, "--")
			expanded = append(expanded, args[2:]...)
		}

		return expanded, isShell, nil
	}

	// Split before filling the placeholders, so that an argument is never
	// parsed again: its quotes, spaces and "$N" text reach the command as is.
	newArgs, err := shlex.Split(expansion)
	if err != nil {
		return nil, false, err
	}

	aliasArgs := args[2:]
	fill := func(s string) string {
		return placeholderRE.ReplaceAllStringFunc(s, func(p string) string {
			n, _ := strconv.Atoi(p[1:])
			if n > len(aliasArgs) {
				return p
			}
			return aliasArgs[n-1]
		})
	}

	highest := 0
	for _, word := range newArgs {
		for _, m := range placeholderRE.FindAllStringSubmatch(word, -1) {
			n, _ := strconv.Atoi(m[1])
			highest = max(highest, n)
		}
	}
	if highest > len(aliasArgs) {
		return nil, false, fmt.Errorf("not enough arguments for alias: %s", fill(expansion))
	}

	for i, word := range newArgs {
		newArgs[i] = fill(word)
	}

	expanded = slices.Concat(newArgs, aliasArgs[highest:])
	return expanded, false, nil
}

var placeholderRE = regexp.MustCompile(`\$([1-9]\d*)`)

func findSh() (string, error) {
	shPath, err := exec.LookPath("sh")
	if err == nil {
		return shPath, nil
	}

	if runtime.GOOS == "windows" {
		winNotFoundErr := errors.New("unable to locate sh to execute the shell alias with. The sh.exe interpreter is typically distributed with Git for Windows")
		// We can try and find a sh executable in a Git for Windows install
		gitPath, err := exec.LookPath("git")
		if err != nil {
			return "", winNotFoundErr
		}

		shPath = filepath.Join(filepath.Dir(gitPath), "..", "bin", "sh.exe")
		_, err = os.Stat(shPath)
		if err != nil {
			return "", winNotFoundErr
		}

		return shPath, nil
	}

	return "", errors.New("unable to locate sh to execute shell alias with")
}
