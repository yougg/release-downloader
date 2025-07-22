package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"code.gitea.io/sdk/gitea"
	gha "github.com/sethvargo/go-githubactions"
	"golang.org/x/mod/semver"
)

type Reference struct {
	Repository string `json:"repository,omitempty"`
	Prerelease string `json:"prerelease,omitempty"`
	Version    string `json:"version,omitempty"`
	DownloadTo string `json:"downloadTo,omitempty"`
	Sources    string `json:"sources,omitempty"`
	Files      string `json:"files,omitempty"`
	Exclude    string `json:"exclude,omitempty"`
	Single     bool   `json:"-"`
}

var (
	addDot  = strings.NewReplacer(`*`, `.*`)
	uniqDot = strings.NewReplacer(`..*`, `.*`)
	split   = regexp.MustCompile(`[\n,]+`)

	token string
)

func X(s string) string { return fmt.Sprintf("\033[1;91m❌ %s\033[0m", s) }
func V(s string) string { return fmt.Sprintf("\033[1;94m%s\033[0m", s) }

func getInput(k string) string { return strings.TrimSpace(gha.GetInput(k)) }

func main() {
	ctx, err := gha.Context()
	if err != nil {
		gha.Fatalf(X("failed to get context: %v"), err)
		return
	}

	token = getInput(`token`)
	if token == "" {
		token = os.Getenv(`GITEA_TOKEN`)
	}
	if insecure, _ := strconv.ParseBool(getInput(`insecure`)); insecure {
		http.DefaultClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}

	to := getInput(`timeout`)
	if to != `` && to != `0` {
		timeout, err := time.ParseDuration(to)
		if err != nil {
			gha.Fatalf(X("failed to parse timeout: %v"), err)
			return
		}
		http.DefaultClient.Timeout = timeout
	}
	var client *gitea.Client
	for c := range 5 {
		client, err = gitea.NewClient(ctx.ServerURL, gitea.SetToken(token), gitea.SetHTTPClient(http.DefaultClient))
		if err != nil {
			gha.Errorf("failed to create gitea client: %s, retrying...", X(fmt.Sprintf("%v", err)))
			if c == 4 {
				gha.Fatalf(X("create gitea client failed after retry 5 times"))
				return
			}
			time.Sleep(time.Second)
			continue
		}
		break
	}
	if client == nil {
		gha.Fatalf(X("create gitea client failed"))
		return
	}

	batch := getInput(`batch`)
	if batch == `` {
		fetchRelease(client, Reference{
			Repository: getInput(`repository`),
			Prerelease: getInput(`prerelease`),
			Version:    getInput(`version`),
			DownloadTo: getInput(`downloadTo`),
			Sources:    getInput(`sources`),
			Files:      getInput(`files`),
			Exclude:    getInput(`exclude`),
			Single:     true,
		})
	} else {
		var repos []Reference
		err = json.Unmarshal([]byte(batch), &repos)
		if err != nil {
			gha.Fatalf(X("failed to parse batch: %v"), err)
			return
		}
		for _, ref := range repos {
			gha.Group(ref.Repository)
			fetchRelease(client, ref)
			gha.EndGroup()
		}
	}
}

func fetchRelease(client *gitea.Client, ref Reference) {
	repository := strings.Split(ref.Repository, `/`)
	if len(repository) != 2 {
		gha.Fatalf(X("invalid repository: %s"), ref.Repository)
		return
	}
	owner, repo := repository[0], repository[1]
	var prerelease *bool
	if ref.Prerelease == `true` || ref.Prerelease == `false` {
		prerelease = new(bool)
		*prerelease, _ = strconv.ParseBool(ref.Prerelease)
	}
	if ref.Version == `` || ref.Version == `latest` || ref.Version == `LATEST` {
		ref.Version = `*`
	}
	version, err := regexp.Compile(`^` + uniqDot.Replace(addDot.Replace(ref.Version)) + `$`)
	if err != nil {
		gha.Fatalf(X("failed to compile version regexp: %v"), err)
		return
	}
	gha.Infof("repository: %s", V(ref.Repository))
	gha.Infof("prerelease: %s", V(ref.Prerelease))
	gha.Infof("version rule: %s\n", V(ref.Version))
	if ref.Sources == `` && ref.Files == `` {
		gha.Fatalf(X("input both empty sources and files"))
		return
	}

	var releases []*gitea.Release
	page := 1
	for {
		rs, resp, err := client.ListReleases(owner, repo, gitea.ListReleasesOptions{
			ListOptions: gitea.ListOptions{
				Page:     page,
				PageSize: 50,
			},
			IsPreRelease: prerelease,
		})
		if err != nil || resp == nil {
			gha.Fatalf(X("list releases: %v"), err)
			return
		}
		if resp.StatusCode != http.StatusOK {
			gha.Fatalf(X("list releases response: %s"), resp.Status)
			return
		}
		if len(rs) == 0 {
			if len(releases) == 0 {
				gha.Fatalf(X("no releases found in repository"))
				return
			}
			break
		}
		releases = append(releases, rs...)
		page++
	}

	dir := strings.TrimSpace(ref.DownloadTo)
	if dir == `` {
		dir = `.`
	} else {
		err = os.MkdirAll(dir, 0755)
		if err != nil {
			gha.Fatalf(X("failed to create output directory: %v"), err)
			return
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		gha.Fatalf(X("failed to get current directory: %v"), err)
		return
	}

	var allTags, hitTags []string
	hitReleases := make(map[string]*gitea.Release)
	for _, r := range releases {
		allTags = append(allTags, r.TagName)
		if !version.MatchString(r.TagName) {
			continue
		}
		hitTags = append(hitTags, r.TagName)
		hitReleases[r.TagName] = r
	}
	semver.Sort(hitTags)

	if ref.Sources != `` {
		srcRelease := hitReleases[hitTags[len(hitTags)-1]]
		if srcRelease == nil {
			gha.Fatalf(X("no release tag matched version rule"))
			return
		}
		gha.Infof("hit tag for source: %s", V(srcRelease.TagName))
		var gotSrc bool
		var srcURL, srcName string
		// download sources archive
		if ref.Sources == `VERSION.tar.gz` || ref.Sources == `VERSION.zip` {
			switch filepath.Ext(ref.Sources) {
			case `.gz`:
				srcURL = srcRelease.TarURL
				srcName = filepath.Base(srcRelease.TarURL)
			case `.zip`:
				srcURL = srcRelease.ZipURL
				srcName = filepath.Base(srcRelease.ZipURL)
			}
		} else if ref.Sources != `` {
			srcURL = strings.TrimSuffix(srcRelease.TarURL, filepath.Base(srcRelease.TarURL)) + ref.Sources
			srcName = strings.Replace(ref.Sources, `/`, `_`, -1)
		}
		if srcURL != `` && srcName != `` {
			if err = download(srcURL, filepath.Join(dir, srcName), 0); err != nil {
				gha.Fatalf(X("download source archive %s: %v"), srcURL, err)
				return
			}
			gha.Infof("url: %s", V(srcURL))
			gha.Infof("file: %s", V(filepath.Join(wd, dir, srcName)))
			gotSrc = true
		}
		// if downloaded source and no files to be download then return
		if gotSrc && len(ref.Files) == 0 {
			status, commit, err := releaseStatus(client, owner, repo, srcRelease.TagName)
			if err != nil {
				gha.Fatalf(X("get release status: %v"), err)
				return
			}
			gha.Infof("src tag SHA: %s\n", V(status.SHA))
			setOutput(srcRelease, status, commit)
			return
		}
	}

	var release *gitea.Release
	for i := len(hitTags) - 1; i >= 0; i-- {
		r := hitReleases[hitTags[i]]
		if len(r.Attachments) > 0 {
			release = r
			break
		}
		gha.Warningf("no attachment found in release: %s, skip it", r.TagName)
	}
	if release == nil {
		gha.Infof("allTags: %s", V(fmt.Sprintf("%v", allTags)))
		gha.Fatalf(X("no release tag matched version rule or no attachment found in these releases"))
		return
	}
	gha.Infof("hit tag: %s", V(release.TagName))

	status, commit, err := releaseStatus(client, owner, repo, release.TagName)
	if err != nil {
		gha.Fatalf(X("get release status: %v"), err)
		return
	}
	gha.Infof("tag SHA: %s\n", V(status.SHA))

	if len(ref.Files) > 0 && len(release.Attachments) == 0 {
		gha.Fatalf(X("no attachment found in release: %s"), release.TagName)
		return
	}

	fileList := split.Split(ref.Files, -1)
	var excludes []string
	if ref.Exclude != `` {
		excludes = split.Split(ref.Exclude, -1)
	}
	var attachments []string
	noFile := true
	for _, a := range release.Attachments {
		attachments = append(attachments, a.Name)
		var matched bool
		for _, f := range fileList {
			f = strings.TrimSpace(f)
			f = strings.Trim(f, `'`)
			f = strings.Trim(f, `"`)
			f = strings.TrimSpace(f)
			if ok, err := filepath.Match(f, a.Name); err == nil && ok {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for _, exclude := range excludes {
			exclude = strings.TrimSpace(exclude)
			exclude = strings.Trim(exclude, `'`)
			exclude = strings.Trim(exclude, `"`)
			exclude = strings.TrimSpace(exclude)
			if ok, err := filepath.Match(exclude, a.Name); err == nil && ok {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		noFile = false
		if err = download(a.DownloadURL, filepath.Join(dir, a.Name), a.Size); err != nil {
			gha.Fatalf(X("download attachment %s: %v"), a.Name, err)
			return
		}
		gha.Infof("")
		gha.Infof("url: %s", V(a.DownloadURL))
		gha.Infof("file: %s", V(filepath.Join(wd, dir, a.Name)))
		gha.Infof("size: %s", V(byteCountIEC(a.Size)))
		gha.Infof("createAt: %s", V(a.Created.String()))
	}
	if noFile {
		gha.Infof("files rule: %s", V(fmt.Sprintf("%v", fileList)))
		gha.Infof("attachments: %s", V(fmt.Sprintf("%v", attachments)))
		gha.Fatalf(X("no release attachment matched file rule"))
		return
	}

	if !ref.Single {
		return
	}
	setOutput(release, status, commit)
}

func releaseStatus(client *gitea.Client, owner, repo, tagName string) (status *gitea.CombinedStatus, commit *gitea.Commit, err error) {
	var resp *gitea.Response
	status, resp, err = client.GetCombinedStatus(owner, repo, tagName)
	if err != nil || resp == nil {
		gha.Fatalf(X("get tag <%s> status: %v"), tagName, err)
		return
	}
	if resp.StatusCode != http.StatusOK {
		gha.Fatalf(X("get tag <%s> status response: %s"), tagName, resp.Status)
		return
	}
	if status.SHA == `` {
		var tag *gitea.Tag
		tag, resp, err = client.GetTag(owner, repo, tagName)
		if err != nil || resp == nil {
			gha.Fatalf(X("get tag by name <%s>: %v"), tagName, err)
			return
		}
		status.SHA = tag.Commit.SHA
	}
	commit, resp, err = client.GetSingleCommit(owner, repo, status.SHA)
	if err != nil || resp == nil {
		gha.Fatalf(X("get commit by SHA <%s>: %v"), status.SHA, err)
	}
	return
}

func setOutput(release *gitea.Release, status *gitea.CombinedStatus, commit *gitea.Commit) {
	gha.SetOutput(`tag`, release.TagName)
	gha.SetOutput(`url`, release.HTMLURL)
	gha.SetOutput(`sha`, status.SHA)
	gha.SetOutput(`time`, release.PublishedAt.Format(time.DateTime))
	gha.SetOutput(`body`, release.Note)
	gha.SetOutput(`user`, release.Publisher.UserName)
	gha.SetOutput(`status`, string(status.State))
	gha.SetOutput(`stable`, stableMark(release))
	gha.SetOutput(`commit`, commit.HTMLURL)
}

func stableMark(release *gitea.Release) string {
	if release.IsPrerelease || release.IsDraft {
		return ``
	}
	return `✔`
}

// download will download an url and store it in local filepath
func download(url, file string, size int64) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set(`Authorization`, `token `+token)
	// Get the data
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// Create the file
	out, err := os.Create(file)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	// Write the body to file
	n, err := io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	if size > 0 && n != size {
		return fmt.Errorf("download invalid file size, want: %d, got: %d", size, n)
	}

	return nil
}

func byteCountIEC(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	f := fmt.Sprintf("%.2f", float64(b)/float64(div))
	s := strings.TrimSuffix(strings.TrimSuffix(f, `0`), `.0`)
	return fmt.Sprintf("%s %cB", s, "KMGTPE"[exp])
}
