package module

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/gomods/athens/pkg/errors"
	"github.com/gomods/athens/pkg/observ"
	"github.com/gomods/athens/pkg/storage"
	"github.com/gomods/athens/pkg/vanity"
	"github.com/spf13/afero"
)

type goGetFetcher struct {
	fs           afero.Fs
	goBinaryName string
	envVars      []string
	gogetDir     string
	replacements []vanity.Replacement
}

type goModule struct {
	Path     string `json:"path"`     // module path
	Version  string `json:"version"`  // module version
	Error    string `json:"error"`    // error loading module
	Info     string `json:"info"`     // absolute path to cached .info file
	GoMod    string `json:"goMod"`    // absolute path to cached .mod file
	Zip      string `json:"zip"`      // absolute path to cached .zip file
	Dir      string `json:"dir"`      // absolute path to cached source root directory
	Sum      string `json:"sum"`      // checksum for path, version (as in go.sum)
	GoModSum string `json:"goModSum"` // checksum for go.mod (as in go.sum)
}

// NewGoGetFetcher creates fetcher which uses go get tool to fetch modules.
func NewGoGetFetcher(goBinaryName, gogetDir string, envVars []string, fs afero.Fs) (Fetcher, error) {
	const op errors.Op = "module.NewGoGetFetcher"
	if err := validGoBinary(goBinaryName); err != nil {
		return nil, errors.E(op, err)
	}

	return &goGetFetcher{
		fs:           fs,
		goBinaryName: goBinaryName,
		envVars:      envVars,
		gogetDir:     gogetDir,
		replacements: vanity.ReplacementsFromEnv(),
	}, nil
}

func (g *goGetFetcher) copyDir(src string, replacement ...string) error {
	return afero.Walk(g.fs, src, func(ppath string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		var newP string
		if len(replacement) == 1 {
			newP = replacement[0]
		} else if len(replacement) == 2 {
			find, replace := replacement[0], replacement[1]
			newP = strings.Replace(ppath, find, replace, 1)
		}
		if info.IsDir() {
			if err := g.fs.MkdirAll(newP, os.ModeDir|os.ModePerm); err != nil {
				return err
			}
			return nil
		}
		f, err := g.fs.Create(newP)
		if err != nil {
			return err
		}
		defer f.Close()
		oF, err := g.fs.Open(ppath)
		if err != nil {
			return err
		}
		defer oF.Close()

		_, err = io.Copy(f, oF)
		if err != nil {
			return err
		}

		return nil
	})
}

// Fetch downloads the sources from the go binary and returns the corresponding
// .info, .mod, and .zip files.
func (g *goGetFetcher) Fetch(ctx context.Context, mod, ver string) (*storage.Version, error) {
	const op errors.Op = "goGetFetcher.Fetch"
	ctx, span := observ.StartSpan(ctx, op.String())
	defer span.End()

	// setup the GOPATH
	goPathRoot, err := afero.TempDir(g.fs, g.gogetDir, "athens")
	if err != nil {
		return nil, errors.E(op, err)
	}
	sourcePath := filepath.Join(goPathRoot, "src")
	modPath := filepath.Join(sourcePath, getRepoDirName(mod, ver))
	if err := g.fs.MkdirAll(modPath, os.ModeDir|os.ModePerm); err != nil {
		_ = clearFiles(g.fs, goPathRoot)
		return nil, errors.E(op, err)
	}

	m, err := downloadModule(
		ctx,
		g.goBinaryName,
		g.envVars,
		goPathRoot,
		modPath,
		mod,
		ver,
	)
	if err != nil {
		_ = clearFiles(g.fs, goPathRoot)
		return nil, errors.E(op, err)
	}

	var storageVer storage.Version
	storageVer.Semver = m.Version
	info, err := afero.ReadFile(g.fs, m.Info)
	if err != nil {
		return nil, errors.E(op, err)
	}
	modInfo := struct {
		Version string `json:"Version"`
		Time    string `json:"Time"`
		Origin  *struct {
			VCS  string `json:"VCS,omitempty"`
			URL  string `json:"URL,omitempty"`
			Hash string `json:"Hash,omitempty"`
		} `json:"Origin,omitempty"`
	}{}

	for idx := range g.replacements {
		if strings.Contains(m.Path, g.replacements[idx].Replacement) {
			if err := g.copyDir(m.Dir, g.replacements[idx].Replacement, g.replacements[idx].Vanity); err != nil {
				return nil, errors.E(op, err)
			}
			if err := g.copyDir(path.Dir(m.Zip), g.replacements[idx].Replacement, g.replacements[idx].Vanity); err != nil {
				return nil, errors.E(op, err)
			}
			m.Path = strings.Replace(m.Path, g.replacements[idx].Replacement, g.replacements[idx].Vanity, 1)
			m.Info = strings.Replace(m.Info, g.replacements[idx].Replacement, g.replacements[idx].Vanity, 1)
			m.GoMod = strings.Replace(m.GoMod, g.replacements[idx].Replacement, g.replacements[idx].Vanity, 1)
			m.Zip = strings.Replace(m.Zip, g.replacements[idx].Replacement, g.replacements[idx].Vanity, 1)
			m.Dir = strings.Replace(m.Dir, g.replacements[idx].Replacement, g.replacements[idx].Vanity, 1)

			fmt.Println("creating zip archive: ", m.Zip)
			archive, err := g.fs.Create(m.Zip)
			if err != nil {
				return nil, errors.E(op, err)
			}
			zipWriter := zip.NewWriter(archive)
			err = afero.Walk(g.fs, m.Dir, func(ppath string, info fs.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					return nil
				}
				srcF, err := g.fs.Open(ppath)
				if err != nil {
					return err
				}
				defer srcF.Close()

				zipPath := path.Join(g.replacements[idx].Vanity, strings.Split(ppath, g.replacements[idx].Vanity)[1])
				dstF, err := zipWriter.Create(zipPath)
				if err != nil {
					return nil
				}

				if _, err := io.Copy(dstF, srcF); err != nil {
					return err
				}

				return nil
			})
			zipWriter.Close()
			if err != nil {
				return nil, errors.E(op, err)
			}

			if err := json.Unmarshal([]byte(info), &modInfo); err != nil {
				return nil, errors.E(op, err)
			}
			if modInfo.Origin != nil {
				modInfo.Origin.URL = strings.Replace(modInfo.Origin.URL, g.replacements[idx].Replacement, g.replacements[idx].Vanity, 1)
			}
			info, err = json.Marshal(modInfo)
			if err != nil {
				return nil, errors.E(op, err)
			}
			break
		}
	}

	storageVer.Info = info

	gomod, err := afero.ReadFile(g.fs, m.GoMod)
	if err != nil {
		return nil, errors.E(op, err)
	}
	storageVer.Mod = gomod

	zip, err := g.fs.Open(m.Zip)
	if err != nil {
		return nil, errors.E(op, err)
	}
	// note: don't close zip here so that the caller can read directly from disk.
	//
	// if we close, then the caller will panic, and the alternative to make this work is
	// that we read into memory and return an io.ReadCloser that reads out of memory
	storageVer.Zip = &zipReadCloser{zip, g.fs, goPathRoot}

	return &storageVer, nil
}

// given a filesystem, gopath, repository root, module and version, runs 'go mod download -json'
// on module@version from the repoRoot with GOPATH=gopath, and returns a non-nil error if anything went wrong.
func downloadModule(
	ctx context.Context,
	goBinaryName string,
	envVars []string,
	gopath,
	repoRoot,
	module,
	version string,
) (goModule, error) {
	const op errors.Op = "module.downloadModule"

	uri := strings.TrimSuffix(module, "/")
	fullURI := fmt.Sprintf("%s@%s", uri, version)

	cmd := exec.CommandContext(ctx, goBinaryName, "mod", "download", "-json", fullURI)
	cmd.Env = prepareEnv(gopath, envVars)
	cmd.Dir = repoRoot
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if err != nil {
		err = fmt.Errorf("%w: %s", err, stderr)
		var m goModule
		if jsonErr := json.NewDecoder(stdout).Decode(&m); jsonErr != nil {
			return goModule{}, errors.E(op, err)
		}
		// github quota exceeded
		if isLimitHit(m.Error) {
			return goModule{}, errors.E(op, m.Error, errors.KindRateLimit)
		}
		return goModule{}, errors.E(op, m.Error, errors.KindNotFound)
	}

	var m goModule
	if err = json.NewDecoder(stdout).Decode(&m); err != nil {
		return goModule{}, errors.E(op, err)
	}
	if m.Error != "" {
		return goModule{}, errors.E(op, m.Error)
	}

	return m, nil
}

func isLimitHit(o string) bool {
	return strings.Contains(o, "403 response from api.github.com")
}

// getRepoDirName takes a raw repository URI and a version and creates a directory name that the
// repository contents can be put into.
func getRepoDirName(repoURI, version string) string {
	escapedURI := strings.ReplaceAll(repoURI, "/", "-")
	return fmt.Sprintf("%s-%s", escapedURI, version)
}

func validGoBinary(name string) error {
	const op errors.Op = "module.validGoBinary"
	err := exec.Command(name).Run()
	eErr := &exec.ExitError{}
	if err != nil && !errors.AsErr(err, &eErr) {
		return errors.E(op, err)
	}
	return nil
}
