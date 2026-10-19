package checkout

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	// flag values
	branch   string
	track    bool
	upstream string
	force    bool
	worktree string

	// dependencies captured from factory
	factory      cmdutils.Factory
	io           *iostreams.IOStreams
	gr           git.GitRunner
	gitlabClient func() (*gitlab.Client, error)
	remotes      func() (glrepo.Remotes, error)
	config       func() config.Config

	// resolved by complete before invoking run
	mr       *gitlab.MergeRequest
	baseRepo glrepo.Interface

	// set by validate() when --worktree already points at this branch's worktree
	worktreeInPlace bool
}

func NewCmdCheckout(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		factory:      f,
		io:           f.IO(),
		gr:           f.GitRunner(),
		gitlabClient: f.GitLabClient,
		remotes:      f.Remotes,
		config:       f.Config,
	}

	cmd := &cobra.Command{
		Use:   "checkout [<id> | <branch> | <url>]",
		Short: "Check out an open merge request.",
		Long: heredoc.Docf(`
			Defaults to the currently checked-out branch. Use %[1]s--branch%[1]s to
			override the local branch name used for the checkout.

			Use %[1]s--worktree%[1]s to check out the merge request into a Git worktree at the given path. Your current working directory and any work in progress stay untouched. If you run the command again with the same path, it updates the existing worktree.

			You can only check out a branch in one worktree at a time. If a branch is already checked out elsewhere, use %[1]s--branch%[1]s to check it out under a different name.

			To remove the worktree when you are done, run %[1]sgit worktree remove <path>%[1]s.
		`, "`"),
		Example: heredoc.Doc(`
			glab mr checkout 1
			glab mr checkout branch
			glab mr checkout 12 --branch todo-fix
			glab mr checkout new-feature --set-upstream-to=upstream/main
			glab mr checkout https://gitlab.com/gitlab-org/cli/-/merge_requests/1234

			# Check out into a separate worktree
			glab mr checkout 123 --worktree ../review-mr-123

			# Uses the checked-out branch
			glab mr checkout`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := opts.complete(ctx, args); err != nil {
				return err
			}
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(ctx)
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&opts.branch, "branch", "b", "", "Check out merge request with name <branch>.")
	fl.BoolVarP(&opts.track, "track", "t", true, "Set checked out branch to track the remote branch.")
	_ = fl.MarkDeprecated("track", "Now enabled by default")
	fl.StringVarP(&opts.upstream, "set-upstream-to", "u", "", "Set tracking of checked-out branch to [REMOTE/]BRANCH.")
	fl.BoolVarP(&opts.force, "force", "f", false, "Reset local branch to remote when they have diverged. Refuses if working tree has changes that would be lost.")
	fl.StringVar(&opts.worktree, "worktree", "", "Check out the merge request into a Git worktree at <path> instead of the current working directory. Re-running updates an existing worktree at that path.")
	return cmd
}

func (o *options) complete(ctx context.Context, args []string) error {
	mr, baseRepo, err := mrutils.MRFromArgs(ctx, o.factory, args, "any")
	if err != nil {
		return err
	}
	if mr.SHA == "" {
		return fmt.Errorf("merge request !%d has no head commit", mr.IID)
	}
	o.mr = mr
	o.baseRepo = baseRepo

	if o.branch == "" {
		o.branch = o.mr.SourceBranch
	}
	if err := validateBranchName(o.branch); err != nil {
		return err
	}

	return nil
}

func validateBranchName(name string) error {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return fmt.Errorf("invalid branch name %q", name)
	}
	return nil
}

func (o *options) validate() error {
	if o.upstream != "" {
		if val := strings.Split(o.upstream, "/"); len(val) > 1 {
			// Verify that we have the remote set
			repo, err := o.remotes()
			if err != nil {
				return err
			}
			_, err = repo.FindByName(val[0])
			if err != nil {
				return err
			}
		}
	}

	if o.worktree != "" {
		existing, prunable, err := o.worktreeForBranch()
		if err != nil {
			return err
		}
		switch {
		case existing == "":
		case prunable:
			return fmt.Errorf("branch %q is held by a worktree at %q whose directory is gone; run `git worktree prune`, then retry", o.branch, existing)
		case sameWorktreePath(existing, o.worktree):
			o.worktreeInPlace = true
		default:
			return fmt.Errorf("branch %q is already checked out at %q; use that worktree, or pass --branch to check it out under a different name", o.branch, existing)
		}
	}

	return nil
}

// worktreeForBranch returns the path of the worktree that already has o.branch
// checked out, and whether that worktree is prunable: still listed, but with its
// directory gone. `git worktree add` refuses a branch that is checked out
// anywhere, prunable or not, so this runs in validate() to fail before the fetch
// and the config writes.
func (o *options) worktreeForBranch() (string, bool, error) {
	out, err := o.gr.Git("worktree", "list", "--porcelain")
	if err != nil {
		return "", false, err
	}

	// Blocks of `worktree <path>` / `HEAD <sha>` / `branch refs/heads/<name>`,
	// with the branch line absent for a detached HEAD and a `prunable` line
	// appended once the directory is gone. That line follows the branch line, so
	// each block is only decided at the next `worktree` line or at the end.
	var path string
	var match, prunable bool
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "worktree "):
			if match {
				return path, prunable, nil
			}
			path = strings.TrimPrefix(line, "worktree ")
			match, prunable = false, false
		case line == "branch refs/heads/"+o.branch:
			match = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			prunable = true
		}
	}
	if match {
		return path, prunable, nil
	}
	return "", false, nil
}

// resolveWorktreePath makes a path comparable with the ones `git worktree list`
// prints, which have their symlinks resolved. EvalSymlinks fails on a path that
// does not exist yet, the ordinary first-run case, so fall back to the cleaned
// absolute path.
func resolveWorktreePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return filepath.Clean(abs)
}

// sameWorktreePath reports whether a path listed by git is the same place as the
// --worktree argument. Both sides are resolved: git usually reports the resolved
// path, but the reverse happens too.
func sameWorktreePath(listed, requested string) bool {
	return resolveWorktreePath(listed) == resolveWorktreePath(requested)
}

func (o *options) run(ctx context.Context) error {
	if o.worktreeInPlace {
		o.io.LogInfof("Branch %q is already checked out at %q, updating it there.\n", o.branch, o.worktree)
	}

	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	var mrRef string
	var mrProject *gitlab.Project

	mrProject, err = api.GetProject(client, o.mr.SourceProjectID)
	if err != nil {
		// If we don't have access to the source project, let's try the target project
		mrProject, err = api.GetProject(client, o.mr.TargetProjectID)
		if err != nil {
			return err
		}

		// We found the target project, let's find the ref another way
		mrRef = fmt.Sprintf("refs/merge-requests/%d/head", o.mr.IID)
	} else {
		mrRef = fmt.Sprintf("refs/heads/%s", o.mr.SourceBranch)
	}

	// Get the appropriate remote URL based on user's git_protocol preference
	gitProtocol, err := o.config().Get(o.baseRepo.RepoHost(), "git_protocol")
	if err != nil {
		return err
	}
	fetchRemote, err := glrepo.RemoteURL(mrProject, gitProtocol)
	if err != nil {
		return err
	}

	// Git only writes refs/remotes/<remote>/<branch> when it fetches through a
	// named remote, so a URL fetch leaves branch.<name>.remote with nothing for
	// @{u} to resolve to.
	if strings.HasPrefix(mrRef, "refs/heads/") {
		if remoteName := o.matchingRemoteName(mrProject); remoteName != "" {
			fetchRemote = remoteName
		}
	}

	localSHA := o.localBranchSHA()
	switch {
	case localSHA == o.mr.SHA:
		o.io.LogInfof("Branch %q already at merge request head, skipping fetch.\n", o.branch)
	case localSHA == "" && o.mrHeadIsLocal():
		if _, err := o.gr.Git("branch", o.branch, o.mr.SHA); err != nil {
			return err
		}
		o.io.LogInfof("Created branch %q from local commit, skipping fetch.\n", o.branch)
	default:
		// git always refuses to fetch into a branch a worktree holds, so an in-place
		// update goes straight to the fallback rather than printing that refusal on
		// every re-run.
		if o.worktreeInPlace {
			if err := o.fetchRefOnly(ctx, fetchRemote, mrRef); err != nil {
				return err
			}
			break
		}
		fetchRefSpec := fmt.Sprintf("%s:%s", mrRef, o.branch)
		if err := o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "fetch", "--", fetchRemote, fetchRefSpec); err != nil {
			// Remote diverged from local. Fall back to fetching just the ref (FETCH_HEAD only).
			if err := o.fetchRefOnly(ctx, fetchRemote, mrRef); err != nil {
				return err
			}
		}
	}

	// .remote is needed for `git pull` to work
	// .pushRemote is needed for `git push` to work, if user has set `remote.pushDefault`.
	// see https://git-scm.com/docs/git-config#Documentation/git-config.txt-branchltnamegtremote
	if _, err := o.gr.Git("config", fmt.Sprintf("branch.%s.remote", o.branch), fetchRemote); err != nil {
		return err
	}
	if o.mr.AllowCollaboration {
		if _, err := o.gr.Git("config", fmt.Sprintf("branch.%s.pushRemote", o.branch), fetchRemote); err != nil {
			return err
		}
	}
	if _, err := o.gr.Git("config", fmt.Sprintf("branch.%s.merge", o.branch), mrRef); err != nil {
		return err
	}

	switch {
	case o.worktreeInPlace:
		// Already checked out at o.worktree; resolveDivergence updated it.
	case o.worktree != "":
		if err := o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "worktree", "add", "--", o.worktree, o.branch); err != nil {
			return fmt.Errorf("could not add worktree %q for branch %q: %w", o.worktree, o.branch, err)
		}
	default:
		if err := o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "checkout", o.branch); err != nil {
			return fmt.Errorf("could not checkout branch %q: %w", o.branch, err)
		}
	}

	if o.upstream != "" {
		// Name the branch explicitly: with --worktree it is not the current branch here.
		if _, err := o.gr.Git("branch", "--set-upstream-to", o.upstream, o.branch); err != nil {
			return err
		}
	}
	return nil
}

// fetchRefOnly fetches the merge request ref without updating the local branch,
// leaving the result in FETCH_HEAD for resolveDivergence to apply.
func (o *options) fetchRefOnly(ctx context.Context, fetchRemote, mrRef string) error {
	if err := o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "fetch", "--", fetchRemote, mrRef); err != nil {
		return err
	}
	return o.resolveDivergence(ctx)
}

// localBranchSHA returns the commit refs/heads/<branch> points at, or "" when
// the branch does not exist.
func (o *options) localBranchSHA() string {
	sha, err := o.gr.Git("rev-parse", "--verify", "refs/heads/"+o.branch)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(sha)
}

// mrHeadIsLocal reports whether the merge request head commit is already in
// the object store, so a branch can be created from it without fetching.
func (o *options) mrHeadIsLocal() bool {
	_, err := o.gr.Git("rev-parse", "--verify", o.mr.SHA+"^{commit}")
	return err == nil
}

// matchingRemoteName returns the name of a locally configured git remote that
// points at mrProject, or an empty string if none matches.
func (o *options) matchingRemoteName(mrProject *gitlab.Project) string {
	remotes, err := o.remotes()
	if err != nil {
		dbg.Debug("could not read git remotes:", err.Error())
		return ""
	}
	mrRepo, err := glrepo.FromFullName(mrProject.PathWithNamespace, o.baseRepo.RepoHost(), o.config())
	if err != nil {
		dbg.Debug("could not resolve merge request project:", err.Error())
		return ""
	}
	for _, remote := range remotes {
		if glrepo.IsSame(remote.Repo, mrRepo) {
			return remote.Name
		}
	}
	return ""
}

// resolveDivergence is called after the fallback fetch wrote FETCH_HEAD but the
// local branch ref was not updated. Decides whether to reset, ask the user, or
// abort. The force flag bypasses the user prompt; otherwise an interactive
// prompt confirms (TTY) or a FlagError is returned (non-TTY).
func (o *options) resolveDivergence(ctx context.Context) error {
	fetched, diverged, err := o.localDivergesFromFetchHead()
	if err != nil {
		return err
	}
	if !diverged {
		return nil
	}

	// A local branch that is simply behind, which is what a checked-out branch
	// looks like after the MR gained commits, has nothing to lose: fast-forward it
	// instead of asking to reset.
	if o.localIsAncestorOfFetchHead() {
		return o.fastForward(fetched)
	}

	onTarget, err := o.guardReset(fetched)
	if err != nil {
		return err
	}

	if !o.force {
		if !o.io.PromptEnabled() {
			return cmdutils.FlagError{Err: fmt.Errorf("local branch %q has diverged from remote. Pass --force to reset it (discards local commits on this branch)", o.branch)}
		}
		var confirmed bool
		if err := o.io.Confirm(ctx, &confirmed, o.divergencePrompt()); err != nil {
			return err
		}
		if !confirmed {
			return cmdutils.CancelError()
		}
	}
	return o.applyReset(onTarget, fetched)
}

// divergencePrompt wording depends on the mode. In worktree mode nothing in
// the current directory changes, only the branch ref moves, so the default
// wording would describe the very thing --worktree exists to avoid. An existing
// worktree is named, since that is where the reviewer will see the change.
func (o *options) divergencePrompt() string {
	if o.worktreeInPlace {
		return fmt.Sprintf("Local branch %q has diverged from remote. Reset it in the worktree at %q (discards local commits on this branch, current directory untouched)?", o.branch, o.worktree)
	}
	if o.worktree != "" {
		return fmt.Sprintf("Local branch %q has diverged from remote. Reset the branch before creating the worktree (discards local commits on this branch, current directory untouched)?", o.branch)
	}
	return fmt.Sprintf("Local branch %q has diverged from remote. Reset to remote (discards local commits on this branch)?", o.branch)
}

// localDivergesFromFetchHead reports whether the local branch ref points at a
// different commit than FETCH_HEAD. A missing local branch is treated as "no
// divergence" so downstream code surfaces any error.
func (o *options) localDivergesFromFetchHead() (string, bool, error) {
	localSHA := o.localBranchSHA()
	if localSHA == "" {
		return "", false, nil
	}
	fetchSHA, err := o.gr.Git("rev-parse", "FETCH_HEAD^{commit}")
	if err != nil {
		return "", false, err
	}
	fetched := strings.TrimSpace(fetchSHA)
	return fetched, localSHA != fetched, nil
}

// localIsAncestorOfFetchHead reports whether FETCH_HEAD descends from the local
// branch, so the local branch can be fast-forwarded without losing commits.
func (o *options) localIsAncestorOfFetchHead() bool {
	_, err := o.gr.Git("merge-base", "--is-ancestor", "refs/heads/"+o.branch, "FETCH_HEAD")
	return err == nil
}

// fastForward moves the local branch to the fetched commit. On the checked-out
// branch it goes through `git merge --ff-only`, which updates the working tree
// and refuses if uncommitted changes conflict with the merge.
//
// git refuses `branch -f` for a branch a worktree holds, so an in-place update
// has to run inside that worktree. FETCH_HEAD is per-worktree and that one has
// its own copy, or none, so the commit is named explicitly.
func (o *options) fastForward(fetched string) error {
	var err error
	if o.worktreeInPlace {
		err = o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "-C", o.worktree, "merge", "--ff-only", fetched)
		if err != nil {
			return fmt.Errorf("could not fast-forward branch %q in the worktree at %q: %w", o.branch, o.worktree, err)
		}
		return nil
	}

	currentBranch, symErr := o.gr.Git("symbolic-ref", "--quiet", "--short", "HEAD")
	if symErr == nil && strings.TrimSpace(currentBranch) == o.branch {
		err = o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "merge", "--ff-only", "FETCH_HEAD")
	} else {
		err = o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "branch", "-f", o.branch, "FETCH_HEAD")
	}
	if err != nil {
		return fmt.Errorf("could not fast-forward local branch %q to the merge request: %w", o.branch, err)
	}
	return nil
}

// guardReset checks whether `git reset --hard FETCH_HEAD` is safe to run for
// the target branch. Returns whether HEAD is currently the target branch, or an
// error describing why the reset would destroy uncommitted work.
func (o *options) guardReset(fetched string) (bool, error) {
	if o.worktreeInPlace {
		// The reset lands in the review worktree, so the dirty check belongs there
		// rather than here. --force must not discard work the reviewer cannot see.
		unsafe, err := o.unsafeToReset(o.worktree, fetched)
		if err != nil {
			return false, err
		}
		if unsafe {
			return false, fmt.Errorf("the worktree at %q has changes that would be lost by reset. Commit, stash, or remove them before resetting branch %q", o.worktree, o.branch)
		}
		return false, nil
	}
	if o.worktree != "" {
		// validate() established that the branch is checked out nowhere, so
		// applyReset moves the ref and the later `worktree add` picks up the head.
		return false, nil
	}

	currentBranch, symErr := o.gr.Git("symbolic-ref", "--quiet", "--short", "HEAD")
	onTarget := symErr == nil && strings.TrimSpace(currentBranch) == o.branch
	if !onTarget {
		return false, nil
	}
	unsafe, err := o.unsafeToReset("", fetched)
	if err != nil {
		return false, err
	}
	if unsafe {
		return false, fmt.Errorf("working tree has changes that would be lost by reset. Commit, stash, or remove them before resetting branch %q", o.branch)
	}
	return true, nil
}

// applyReset mutates the local ref to match FETCH_HEAD. Callers must invoke
// guardReset first and use its returned onTarget value.
func (o *options) applyReset(onTarget bool, fetched string) error {
	if o.worktreeInPlace {
		return o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "-C", o.worktree, "reset", "--hard", fetched)
	}
	if onTarget {
		return o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "reset", "--hard", "FETCH_HEAD")
	}
	return o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "branch", "-f", o.branch, "FETCH_HEAD")
}

// unsafeToReset reports whether `git reset --hard` to the fetched commit would
// silently destroy uncommitted work. Two conditions trigger it:
//  1. Tracked files with modified or staged changes (reset --hard discards them).
//  2. Untracked files whose paths appear in the fetched tree (reset --hard
//     overwrites them silently — unlike `git checkout`, which refuses).
//
// Unrelated untracked files (local notes, build artifacts) are NOT a blocker
// since reset --hard leaves them alone.
// dir selects the working tree to inspect: empty for this one, or the review
// worktree, whose own FETCH_HEAD is unrelated, so the commit is named instead.
func (o *options) unsafeToReset(dir, fetched string) (bool, error) {
	run := func(args ...string) (string, error) {
		if dir != "" {
			args = append([]string{"-C", dir}, args...)
		}
		return o.gr.Git(args...)
	}

	tracked, err := run("diff", "--name-only", "HEAD")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(tracked) != "" {
		return true, nil
	}
	untracked, err := run("ls-files", "--others", "--exclude-standard")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(untracked) == "" {
		return false, nil
	}
	incoming, err := run("ls-tree", "-r", "--name-only", fetched)
	if err != nil {
		return false, err
	}
	incomingPaths := map[string]struct{}{}
	for line := range strings.SplitSeq(incoming, "\n") {
		if line != "" {
			incomingPaths[line] = struct{}{}
		}
	}
	for line := range strings.SplitSeq(untracked, "\n") {
		if line == "" {
			continue
		}
		if _, conflict := incomingPaths[line]; conflict {
			return true, nil
		}
	}
	return false, nil
}
