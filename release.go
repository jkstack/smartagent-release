package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/ast"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
	"github.com/google/go-github/github"
	"github.com/lwch/runtime"
	"github.com/tdewolff/minify/v2"
	htmlmini "github.com/tdewolff/minify/v2/html"
	"golang.org/x/oauth2"
)

func main() {
	isPlugin := flag.Bool("plugin", false, "is project is plugin")
	flag.Parse()

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
	sha, ok := os.LookupEnv("GITHUB_SHA")
	if !ok {
		fmt.Println("Missing GITHUB_SHA env")
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
	releaseID := createOrDrop(gcli, owner, repo, sha, version, changelog)

	if !*isPlugin {
		files, err := filepath.Glob(filepath.Join("release", version, "*"))
		runtime.Assert(err)
		for _, file := range files {
			fi, err := os.Stat(file)
			runtime.Assert(err)
			if fi.IsDir() {
				continue
			}
			upload(gcli, owner, repo, releaseID, file)
		}
	}

	pack("release", version)
	upload(gcli, owner, repo, releaseID, "v"+version+".tar.gz")
}

func createOrDrop(cli *github.Client, owner, repo, sha, version, body string) int64 {
	log.Printf("create release %s...", version)

	branch := "v" + version
	version = "v" + version

	log.Println("delete old tag...")
	r, _ := cli.Git.DeleteRef(context.Background(), "jkstack", "smartagent", "tags/"+branch)
	if r != nil {
		defer r.Body.Close()
	}

	log.Println("delete old branch...")
	r, _ = cli.Git.DeleteRef(context.Background(), "jkstack", "smartagent", "branches/"+branch)
	if r != nil {
		defer r.Body.Close()
	}

	rel, rep, err := cli.Repositories.GetReleaseByTag(context.Background(), owner, repo, branch)
	if err == nil {
		defer rep.Body.Close()
		log.Println("old release found, delete...")
		r, _ := cli.Repositories.DeleteRelease(context.Background(), owner, repo, rel.GetID())
		if r != nil {
			defer r.Body.Close()
		}
	}

	log.Printf("create tag %s sha %s...", version, sha)
	msg := "auto create branch " + branch
	t := "commit"
	var tag github.Tag
	tag.Tag = &version
	tag.Message = &msg
	tag.Object = &github.GitObject{
		Type: &t,
		SHA:  &sha,
	}
	_, resp, err := cli.Git.CreateTag(context.Background(), owner, repo, &tag)
	runtime.Assert(err)
	defer resp.Body.Close()

	log.Printf("create release %s...", version)
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

func pack(dir, version string) {
	log.Printf("packing for v%s.tar.gz...", version)
	f, err := os.Create("v" + version + ".tar.gz")
	runtime.Assert(err)
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	err = filepath.Walk(dir, func(path string, info fs.FileInfo, err error) error {
		if path == "." || path == ".." {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = strings.TrimPrefix(path, dir)
		log.Printf("added file %s", hdr.Name)
		err = tw.WriteHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	runtime.Assert(err)
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

	root := markdown.Parse(data, parser.New())
	list := root.GetChildren()
	if len(list) > 0 {
		if !isChangeLog(list[0]) {
			panic("is not CHANGELOG.md")
		}
	}
	doc := new(ast.Document)
	render := func() string {
		data := markdown.Render(doc, html.NewRenderer(html.RendererOptions{}))
		m := minify.New()
		m.AddFunc("text/html", htmlmini.Minify)
		dt, err := m.Bytes("text/html", data)
		if err == nil {
			return string(dt)
		}
		return string(data)
	}
	var nodes []ast.Node
	var latest Version
	for _, node := range list[1:] {
		ver, ok := isVersion(node)
		if ok {
			if len(nodes) > 0 {
				doc.SetChildren(nodes)
				if latest.String() == version {
					return render()
				}
			}
			nodes = nodes[:0]
			latest = ver
		}
		nodes = append(nodes, node)
	}
	if len(nodes) > 0 {
		doc.SetChildren(nodes)
		return render()
	}

	return ""
}

func getContent(node ast.Node) string {
	contentToString := func(a, b []byte) string {
		if len(a) > 0 {
			return string(a)
		}
		if len(b) > 0 {
			return string(b)
		}
		return ""
	}
	if c := node.AsContainer(); c != nil {
		return contentToString(c.Literal, c.Content)
	}
	leaf := node.AsLeaf()
	return contentToString(leaf.Literal, leaf.Content)
}

func isChangeLog(node ast.Node) bool {
	if _, ok := node.(*ast.Heading); !ok {
		return false
	}
	list := node.GetChildren()
	if len(list) == 0 {
		return false
	}
	return getContent(list[0]) == "CHANGELOG"
}

func isVersion(node ast.Node) (Version, bool) {
	var ver Version
	if _, ok := node.(*ast.Heading); !ok {
		return ver, false
	}
	list := node.GetChildren()
	if len(list) == 0 {
		return ver, false
	}
	var err error
	ver, err = ParseVersion(getContent(list[0]))
	if err == nil {
		return ver, true
	}
	return ver, false
}

type Version struct {
	data [3]int
}

func ParseVersion(str string) (Version, error) {
	var ret Version
	tmp := strings.SplitN(str, ".", 3)
	if len(tmp) != 3 {
		return ret, errors.New("invalid version")
	}
	n, err := strconv.ParseInt(tmp[0], 10, 64)
	if err != nil {
		return ret, errors.New("invalid major version")
	}
	ret.data[0] = int(n)
	n, err = strconv.ParseInt(tmp[1], 10, 64)
	if err != nil {
		return ret, errors.New("invalid minor version")
	}
	ret.data[1] = int(n)
	n, err = strconv.ParseInt(tmp[2], 10, 64)
	if err != nil {
		return ret, errors.New("invalid patch version")
	}
	ret.data[2] = int(n)
	return ret, nil
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.data[0], v.data[1], v.data[2])
}
