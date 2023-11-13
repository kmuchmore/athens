package gopkg

import (
	"fmt"
	"net/http"
	"strings"
)

type Vanity struct{}

func NewVanity() Vanity {
	return Vanity{}
}

func (v Vanity) ReplaceVanity(path string, req *http.Request) (module string, version string, err error) {
	newPath := strings.Replace(path, "gopkg.in", "", 1)
	repo, err := v.handle(newPath)
	if err != nil {
		return "", "", err
	}

	if repo.MajorVersion.Major > 1 {
		return repo.GitHubRoot() + "/" + repo.MajorVersion.String(), repo.GitHubTree(), nil
	}
	return repo.GitHubRoot(), repo.GitHubTree(), nil

}
func (v Vanity) RestoreVanity(path string, extra ...interface{}) (string, error) {

	return "", nil
}
func (v Vanity) handle(path string) (*Repo, error) {
	m := patternNew.FindStringSubmatch(path)
	oldFormat := false
	if m == nil {
		m = patternOld.FindStringSubmatch(path)
		if m == nil {
			return nil, fmt.Errorf("Unsupported URL pattern; see the documentation at gopkg.in for details.")
		}
		// "/v2/name" <= "/name.v2"
		m[2], m[3] = m[3], m[2]
		oldFormat = true
	}

	if strings.Contains(m[3], ".") {
		return nil, fmt.Errorf("Import paths take the major version only (.%s instead of .%s); see docs at gopkg.in for the reasoning.",
			m[3][:strings.Index(m[3], ".")], m[3])
	}

	repo := &Repo{
		User:        m[1],
		Name:        m[2],
		SubPath:     m[4],
		OldFormat:   oldFormat,
		FullVersion: InvalidVersion,
	}

	var ok bool
	repo.MajorVersion, ok = parseVersion(m[3])
	if !ok {
		return nil, fmt.Errorf("Version %q improperly considered invalid; please warn the service maintainers.", m[3])
	}

	var changed []byte
	var versions VersionList
	original, err := fetchRefs(repo)
	if err == ErrTimeout {
		// Retry once.
		httpClient.CloseIdleConnections()
		original, err = fetchRefs(repo)
	}
	if err == nil {
		changed, versions, err = changeRefs(original, repo.MajorVersion)
		repo.SetVersions(versions)
	}

	_ = changed
	return repo, nil
}
