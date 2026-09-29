package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/binbandit/yip/internal/forge"
)

// ErrCLIUnavailable means the GitHub CLI (gh) isn't installed or isn't
// signed in on this machine.
var ErrCLIUnavailable = errors.New("github: the GitHub CLI (gh) is not installed or not signed in")

// ghExitAuth is gh's exit status when it needs `gh auth login`.
const ghExitAuth = 4

// ViewWithCLI reads a github.com repository through the GitHub CLI (`gh repo
// view`), as whichever account gh is signed in as on this machine, so a
// private repository that account can see needs no token stored in yip. A
// repository the account can't see is forge.ErrNotFound.
func ViewWithCLI(ctx context.Context, owner, name string) (RepoInfo, error) {
	if !validRepoName(owner, name) {
		return RepoInfo{}, fmt.Errorf("github: invalid repository %q/%q", owner, name)
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return RepoInfo{}, ErrCLIUnavailable
	}
	// The host is explicit so a GH_HOST or the working directory's remote
	// can't point the lookup at another GitHub.
	cmd := exec.CommandContext(ctx, gh, "repo", "view", defaultHost+"/"+owner+"/"+name, "--json", "name,owner,defaultBranchRef,isPrivate")
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		var exit *exec.ExitError
		switch {
		case ctx.Err() != nil:
			return RepoInfo{}, ctx.Err()
		case errors.As(err, &exit) && exit.ExitCode() == ghExitAuth:
			return RepoInfo{}, fmt.Errorf("%w: %s", ErrCLIUnavailable, msg)
		case strings.Contains(msg, "Could not resolve to a Repository"):
			return RepoInfo{}, fmt.Errorf("%w: %s/%s: %s", forge.ErrNotFound, owner, name, msg)
		}
		return RepoInfo{}, fmt.Errorf("github: gh repo view %s/%s: %v: %s", owner, name, err, msg)
	}
	var v struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
		IsPrivate bool `json:"isPrivate"`
	}
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		return RepoInfo{}, fmt.Errorf("github: decode gh repo view: %w", err)
	}
	info := RepoInfo{Owner: owner, Name: name, DefaultBranch: v.DefaultBranchRef.Name, Private: v.IsPrivate}
	if v.Owner.Login != "" && v.Name != "" {
		info.Owner, info.Name = v.Owner.Login, v.Name
	}
	return info, nil
}

// CLIToken returns the token the GitHub CLI is signed in with for host
// (`gh auth token`), for storing as the hub's forge credential.
func CLIToken(ctx context.Context, host string) (string, error) {
	gh, err := exec.LookPath("gh")
	if err != nil {
		return "", ErrCLIUnavailable
	}
	cmd := exec.CommandContext(ctx, gh, "auth", "token", "--hostname", host)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", ErrCLIUnavailable, strings.TrimSpace(errb.String()))
	}
	token := strings.TrimSpace(out.String())
	if token == "" {
		return "", ErrCLIUnavailable
	}
	return token, nil
}
