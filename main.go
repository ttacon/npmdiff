package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	hgMode  = flag.Bool("hg", false, "act as if we're in a mercurial repo")
	gitMode = flag.Bool("git", false, "act as if we're in a git repo")
	useExit = flag.Bool("exit-stat", false, "exit w/ non-zero status if diffs found")
)

func main() {
	flag.Parse()

	if !*hgMode && !*gitMode {
		fmt.Fprintln(os.Stderr, "npmdiff: hg mode or git mode must be specified")
		return
	}

	var (
		outBuf = bytes.NewBuffer(nil)
		cmd    *exec.Cmd
	)
	if *hgMode {
		cmd = exec.Command("hg", "root")
	} else {
		cmd = exec.Command("git", "rev-parse", "--show-toplevel")
	}
	cmd.Stdout = outBuf

	if err := cmd.Run(); err != nil {
		if strings.Contains(err.Error(), "exit status 255") {
			// most likely we're not in a mercurial repo
			// NOTE(ttacon): the above comment was written when
			// targetting/testing specifically with hg, need to see what
			// git returns
			fmt.Fprintln(os.Stderr, "npmdiff: not in a repository")
			return
		}
		fmt.Fprintln(os.Stderr, "failed to identify root of repo, err: ", err)
		return
	}

	var (
		toTraverse = []string{outBuf.String()}
		diffsFound = false
		allDiffs   = make(map[string][]string)
		nextDir    = func() string {
			if len(toTraverse) == 0 {
				return ""
			}
			var toReturn = toTraverse[0]
			toTraverse = toTraverse[1:]
			return toReturn
		}
	)

	for dir := nextDir(); dir != ""; dir = nextDir() {
		dir = strings.Trim(dir, "\n")
		fInfo, err := os.Open(dir)
		if err != nil {
			// swallow it?
			continue
		}

		files, err := fInfo.Readdir(-1)
		if err != nil {
			// swallow it?
			continue
		}

		var foundPkgJSON, foundNodeModules bool
		for _, file := range files {
			if file.Name() == "package.json" {
				foundPkgJSON = true
			} else if file.Name() == "node_modules" && file.IsDir() {
				foundNodeModules = true
			} else if file.IsDir() {
				toTraverse = append(toTraverse, filepath.Join(dir, file.Name()))
			}
			if foundPkgJSON && foundNodeModules {
				diffs := npmdiff(dir)
				if len(diffs) > 0 {
					diffsFound = true
					allDiffs[dir] = append(allDiffs[dir], diffs...)
				}
			}
		}

		if foundPkgJSON && !foundNodeModules {
			fmt.Fprintf(os.Stderr,
				"found 'package.json' in %s, but no 'node_modules'\n",
				dir,
			)
			diffsFound = true
		} else if foundNodeModules && !foundPkgJSON {
			fmt.Fprintf(os.Stderr,
				"found 'node_modules' in %s, but no 'package.json'\n",
				dir,
			)
			diffsFound = true
		}
	}

	for k, v := range allDiffs {
		fmt.Printf("differences found in %q:\n", k)
		for i, diff := range v {
			fmt.Printf("[%d] %s\n", i, diff)
		}
	}

	// for use as script inside others
	if diffsFound && *useExit {
		os.Exit(1)
	}
}

func npmdiff(base string) []string {
	devDeps, err := getDevDependencies(filepath.Join(base, "package.json"))
	if err != nil {
		// TODO(ttacon): return it
		return nil
	}

	existingDeps, err := getExistingDependencies(filepath.Join(base, "node_modules"))
	if err != nil {
		// TODO(ttacon): return it
		return nil
	}

	return diffDeps(devDeps, existingDeps)
}

func diffDeps(pkgDeps, localDeps map[string]string) []string {
	var sim = make(map[string]struct{})
	for k, _ := range pkgDeps {
		if _, ok := localDeps[k]; ok {
			sim[k] = struct{}{}
		}
	}

	var diffs []string
	// find pkgDeps diffs
	for k, _ := range pkgDeps {
		if _, ok := sim[k]; !ok {
			diffs = append(diffs,
				fmt.Sprintf(
					"%q is specified in 'package.json' but is not found locally",
					k,
				),
			)
		}
	}

	// find localDeps diffs
	for k, _ := range localDeps {
		if _, ok := sim[k]; !ok {
			diffs = append(diffs,
				fmt.Sprintf(
					"%q found locally but is not specified in 'package.json'",
					k,
				),
			)
		}
	}

	return diffs
}

func getExistingDependencies(ndModLoc string) (map[string]string, error) {
	file, err := os.Open(ndModLoc)
	if err != nil {
		return nil, err
	}

	var existingDeps = make(map[string]string)
	fInfos, err := file.Readdir(-1)
	if err != nil {
		return nil, err
	}
	for _, fInfo := range fInfos {
		if !fInfo.IsDir() {
			// weird, skip it
			continue
		}

		version, err := getPkgVersion(
			filepath.Join(ndModLoc, fInfo.Name(), "package.json"),
		)
		if err != nil {
			return nil, err
		}
		existingDeps[fInfo.Name()] = version
	}
	return existingDeps, nil
}

func getPkgVersion(pkgLoc string) (string, error) {
	pkg, err := getPkgJSON(pkgLoc)
	if err != nil {
		return "", err
	}
	return pkg.Version, nil
}

func getPkgJSON(pkgLoc string) (*PackageJSON, error) {
	var pkg PackageJSON
	dbytes, err := ioutil.ReadFile(pkgLoc)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(dbytes, &pkg)
	return &pkg, err
}

func getDevDependencies(pkgLoc string) (map[string]string, error) {
	pkg, err := getPkgJSON(pkgLoc)
	if err != nil {
		return nil, err
	}
	return pkg.DevDependencies, nil
}

type PackageJSON struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Main        string            `json:"main"`
	Scripts     map[string]string `json:"scripts"`
	Repository  map[string]string `json:"repository"`
	Keywords    []string          `json:"keywords"`
	//	Author          string            `json:"author"`
	License         string            `json:"license"`
	DevDependencies map[string]string `json:"devDependencies"`
}
