package helpers

import (
	"fmt"

	"github.com/jesseduffield/lazygit/pkg/gocui"
	"github.com/jesseduffield/lazygit/pkg/gui/presentation"
	"github.com/jesseduffield/lazygit/pkg/gui/types"
)

// DeploymentsHelper owns the GitHub environment deployment statuses shown in the
// status panel's main view. They're fetched lazily the first time the panel
// shows them for a repo and then cached for the session, so that the per-render
// path stays cheap; a background routine refreshes the cache periodically while
// the panel is visible. All of the cache fields are only ever touched on the UI
// thread, so they need no lock.
type DeploymentsHelper struct {
	c    *HelperCommon
	host *HostHelper

	repoPath string // the repo content belongs to
	content  string // rendered content, valid once fetched
	fetched  bool   // a fetch has completed for repoPath
	fetching bool   // a fetch is currently in flight
}

func NewDeploymentsHelper(c *HelperCommon, host *HostHelper) *DeploymentsHelper {
	return &DeploymentsHelper{c: c, host: host}
}

// Show renders the cached deployments into the status main view, kicking off a
// fetch (behind a "fetching" placeholder) the first time it's called for a repo.
func (self *DeploymentsHelper) Show() {
	self.InvalidateIfRepoChanged()

	if self.fetched {
		self.render(self.content)
		return
	}

	self.render(self.c.Tr.FetchingDeploymentsStatus)
	self.fetch()
}

// Refresh re-fetches the deployments for the current repo, swapping in the new
// content when it arrives without clearing the panel in the meantime. It's a
// no-op unless the status panel is currently showing deployments, so the
// background routine that calls it never hits GitHub while the panel isn't
// visible.
func (self *DeploymentsHelper) Refresh() {
	self.InvalidateIfRepoChanged()

	if !self.statusMainShowsDeployments() {
		return
	}

	self.fetched = false
	self.fetch()
}

// InvalidateIfRepoChanged drops the cache when the current repo no longer
// matches the one the cache was populated for. It's exposed so the status
// controller can keep the cache keyed to the current repo even on the path where
// it shows the dashboard instead of deployments (non-GitHub repos).
func (self *DeploymentsHelper) InvalidateIfRepoChanged() {
	repoPath := self.c.Git().RepoPaths.RepoPath()
	if repoPath != self.repoPath {
		self.repoPath = repoPath
		self.content = ""
		self.fetched = false
		self.fetching = false
	}
}

func (self *DeploymentsHelper) fetch() {
	if self.fetching {
		return
	}
	self.fetching = true

	fetchRepoPath := self.repoPath
	self.c.OnWorker(func(_ gocui.Task) error {
		content := self.fetchContent()

		self.c.OnUIThread(func() error {
			// Discard the result if the user switched repos while we were fetching.
			if self.repoPath != fetchRepoPath {
				return nil
			}
			self.fetching = false
			self.fetched = true
			self.content = content

			// Only render if the status panel is still showing its deployments view;
			// the user may have navigated away or switched to the all-branches log
			// (which renders into the same main view) while we were fetching.
			if self.statusMainShowsDeployments() {
				self.render(content)
			}
			return nil
		})
		return nil
	})
}

// fetchContent runs on a worker. It resolves the GitHub base remote and auth
// token (which shells out to git and reads gh's config) and performs the network
// request, then turns the outcome into a renderable string.
func (self *DeploymentsHelper) fetchContent() string {
	serviceInfo, token, ok := self.host.GithubBaseRemote()
	if !ok {
		return self.c.Tr.DeploymentsNotAuthenticated
	}

	deployments, err := self.c.Git().GitHub.FetchDeployments(&serviceInfo, token)
	switch {
	case err != nil:
		self.c.Log.Error("error fetching deployments from GitHub: " + err.Error())
		return fmt.Sprintf(self.c.Tr.FetchingDeploymentsError, err.Error())
	case len(deployments) == 0:
		return self.c.Tr.NoDeploymentsFound
	default:
		return presentation.GetDeploymentsContent(deployments, self.c.Tr)
	}
}

// statusMainShowsDeployments reports whether the status panel is focused and its
// main view is currently showing the deployments/status content (as opposed to
// the all-branches log, which the user can switch to with a keybinding and which
// renders into the same main view with a different title).
func (self *DeploymentsHelper) statusMainShowsDeployments() bool {
	return self.c.Context().IsCurrent(self.c.Contexts().Status) &&
		self.c.Views().Main.Title == self.c.Tr.StatusTitle
}

func (self *DeploymentsHelper) render(str string) {
	self.c.RenderToMainViews(types.RefreshMainOpts{
		Pair: self.c.MainViewPairs().Normal,
		Main: &types.ViewUpdateOpts{
			Title: self.c.Tr.StatusTitle,
			Task:  types.NewRenderStringTask(str),
		},
	})
}
