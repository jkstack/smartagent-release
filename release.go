package main

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/parser"
	"github.com/google/go-github/github"
	"github.com/lwch/runtime"
	"golang.org/x/oauth2"
)

func main() {
	token, ok := os.LookupEnv("GITHUB_TOKEN")
	if !ok {
		fmt.Println("Missing GITHUB_TOKEN env")
		os.Exit(1)
	}
	repo, ok := os.LookupEnv("GITHUB_REPOSITORY")
	if !ok {
		fmt.Println("Missing GITHUB_REPOSITORY env")
		os.Exit(1)
	}

	tmp := strings.SplitN(repo, "/", 2)
	if len(tmp) != 2 {
		fmt.Println("Invalid repo")
		os.Exit(1)
	}

	owner, repo := tmp[0], tmp[1]

	ac := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: token,
	})
	ocli := oauth2.NewClient(context.Background(), ac)
	gcli := github.NewClient(ocli)

	version := getVersion()
	changelog := getChangeLog(version)

	log.Printf("create release version=%s", version)

	files, err := filepath.Glob(filepath.Join("release", version))
	runtime.Assert(err)

	releaseID := createOrDrop(gcli, owner, repo, version, changelog)
	for _, file := range files {
		fi, err := os.Stat(file)
		runtime.Assert(err)
		if fi.IsDir() {
			continue
		}
		upload(gcli, owner, repo, releaseID, file)
	}
}

func createOrDrop(cli *github.Client, owner, repo, version, body string) int64 {
	log.Printf("create release %s...", version)
	branch := "v" + version
	version = "v" + version
	cli.Git.DeleteRef(context.Background(), owner, repo, "tags/"+branch)
	var release github.RepositoryRelease
	release.TagName = &branch
	release.Name = &version
	release.Body = &body
	ret, rep, err := cli.Repositories.CreateRelease(
		context.Background(), owner, repo, &release)
	runtime.Assert(err)
	defer rep.Body.Close()
	return ret.GetID()
}

func upload(cli *github.Client, owner, repo string, id int64, dir string) {
	log.Printf("upload file %s...", filepath.Base(dir))
	f, err := os.Open(dir)
	runtime.Assert(err)
	defer f.Close()

	var opt github.UploadOptions
	opt.Name = filepath.Base(dir)
	_, rep, err := cli.Repositories.UploadReleaseAsset(
		context.Background(), owner, repo, id, &opt, f)
	runtime.Assert(err)
	defer rep.Body.Close()
}

func getVersion() string {
	var buf bytes.Buffer
	cmd := exec.Command("make", "version")
	cmd.Stdout = &buf
	runtime.Assert(cmd.Run())
	return strings.TrimSpace(buf.String())
}

func getChangeLog(version string) string {
	data, err := ioutil.ReadFile("CHANGELOG.md")
	runtime.Assert(err)

	markdown.Parse(data, parser.New())
	return ""
}
