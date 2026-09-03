package builtin

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const maxGlobMatches = 200

func RunGlob(ctx context.Context, input any) (string, error) {
	return defaultTools.RunGlob(ctx, input)
}

func (t *Tools) RunGlob(ctx context.Context, input any) (string, error) {
	pattern, _ := input.(map[string]any)["pattern"].(string)
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return "", fmt.Errorf("missing pattern")
	}
	if filepath.IsAbs(pattern) || strings.HasPrefix(pattern, "../") {
		return "", fmt.Errorf("glob pattern must stay inside workspace")
	}

	root, err := t.SafePath(".")
	if err != nil {
		return "", err
	}
	matches := make([]string, 0, maxGlobMatches+1)
	err = filepath.WalkDir(root, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if candidate == root {
			return nil
		}
		relative, err := filepath.Rel(root, candidate)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		matched, err := matchGlob(pattern, relative)
		if err != nil {
			return err
		}
		if !matched {
			return nil
		}
		if _, err := t.SafePath(relative); err != nil {
			return nil
		}
		matches = append(matches, relative)
		return nil
	})
	if err != nil {
		return "", err
	}

	sort.Strings(matches)
	if len(matches) > maxGlobMatches {
		matches = append(matches[:maxGlobMatches], "... (more matches omitted; narrow the pattern)")
	}
	return strings.Join(matches, "\n"), nil
}

func matchGlob(pattern, candidate string) (bool, error) {
	return matchGlobParts(strings.Split(pattern, "/"), strings.Split(candidate, "/"))
}

func matchGlobParts(pattern, candidate []string) (bool, error) {
	if len(pattern) == 0 {
		return len(candidate) == 0, nil
	}
	if pattern[0] == "**" {
		matched, err := matchGlobParts(pattern[1:], candidate)
		if err != nil || matched {
			return matched, err
		}
		if len(candidate) == 0 {
			return false, nil
		}
		return matchGlobParts(pattern, candidate[1:])
	}
	if len(candidate) == 0 {
		return false, nil
	}
	matched, err := path.Match(pattern[0], candidate[0])
	if err != nil || !matched {
		return matched, err
	}
	return matchGlobParts(pattern[1:], candidate[1:])
}
