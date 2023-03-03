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
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
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

const retry = 5

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

	tmp := strings.SplitN(repo, "/", 2)
	if len(tmp) != 2 {
		fmt.Println("Invalid repo")
		os.Exit(1)
	}

	fixSafeDir()

	log.Printf("repo=%s", repo)

	owner, repo := tmp[0], tmp[1]

	ac := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: token,
	})
	ocli := oauth2.NewClient(context.Background(), ac)
	gcli := github.NewClient(ocli)

	version := getVersion()
	changelog := getChangeLog(version)

	pack("release", version)

	log.Printf("create release version=%s", version)
	releaseID := createOrDrop(gcli, owner, repo, version, changelog)

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
	upload(gcli, owner, repo, releaseID, "v"+version+".tar.gz")

	uploadOSS(repo, "v"+version+".tar.gz")
}

func fixSafeDir() {
	dir, err := os.Getwd()
	runtime.Assert(err)
	cmd := exec.Command("git", "config", "--global", "--add", "safe.directory", dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runtime.Assert(cmd.Run())
}

func createOrDrop(cli *github.Client, owner, repo, version, body string) int64 {
	branch := "v" + version
	version = "v" + version

	rel, rep, err := cli.Repositories.GetReleaseByTag(context.Background(), owner, repo, branch)
	if err == nil {
		defer rep.Body.Close()
		log.Println("old release found, delete...")
		r, _ := cli.Repositories.DeleteRelease(context.Background(), owner, repo, rel.GetID())
		if r != nil {
			defer r.Body.Close()
		}
	}

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

func deleteIfExists(cli *github.Client, owner, repo string, id int64, name string) bool {
	var opt github.ListOptions
	opt.PerPage = 1000
	assets, rep, err := cli.Repositories.ListReleaseAssets(context.Background(), owner, repo, id, &opt)
	if err != nil {
		return false
	}
	defer rep.Body.Close()
	for _, asset := range assets {
		if *asset.Name == name {
			log.Printf("delete file %s...", name)
			rep, err = cli.Repositories.DeleteReleaseAsset(context.Background(), owner, repo, asset.GetID())
			if err != nil {
				return false
			}
			defer rep.Body.Close()
		}
	}
	return true
}

func upload(cli *github.Client, owner, repo string, id int64, dir string) {
	log.Printf("upload file %s...", dir)
	for i := 0; i < retry; i++ {
		err := uploadFile(cli, owner, repo, id, dir)
		if err == nil {
			return
		}
		log.Printf("upload file %s failed: %v", dir, err)
		deleteIfExists(cli, owner, repo, id, dir)
		time.Sleep(time.Second)
		continue
	}
	panic(fmt.Sprintf("can not upload file: %s", filepath.Base(dir)))
}

func uploadOSS(repo string, dir string) {
	log.Printf("upload to oss...")
	endpoint := os.Getenv("OSS_ENDPOINT")
	key := os.Getenv("OSS_KEY")
	secret := os.Getenv("OSS_SECRET")
	bucket := os.Getenv("OSS_BUCKET")
	if len(endpoint) == 0 || len(key) == 0 || len(secret) == 0 {
		log.Println("no OSS_ENDPOINT or OSS_KEY or OSS_SECRET or OSS_BUCKET set, skiped")
		return
	}
	cli, err := oss.New(endpoint, key, secret)
	runtime.Assert(err)
	bk, err := cli.Bucket(bucket)
	runtime.Assert(err)
	runtime.Assert(bk.PutObjectFromFile(filepath.Join(repo, filepath.Base(dir)), dir))
}

func uploadFile(cli *github.Client, owner, repo string, id int64, dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	var opt github.UploadOptions
	opt.Name = filepath.Base(dir)
	var rep *github.Response
	_, rep, err = cli.Repositories.UploadReleaseAsset(
		context.Background(), owner, repo, id, &opt, f)
	if err != nil {
		return err
	}
	defer rep.Body.Close()
	return nil
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

	err = filepath.Walk(dir, func(path string, info fs.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
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
		hdr.Name = strings.TrimPrefix(hdr.Name, "/")
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
			continue
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
