package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

type installedSurveyProvenance struct {
	HostSHA, ManifestSHA256, AssetSHA256, AppWebTree, HostWebTree string
}

func installedSurveyWebTree(repository, sha string) (string, error) {
	if repository == "" {
		_, file, _, _ := runtime.Caller(0)
		repository = filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	}
	if !filepath.IsAbs(repository) || !validDomesticReleaseSHA(sha) {
		return "", errors.New("invalid installed Survey source repository or SHA")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", repository, "rev-parse", "--verify", sha+":web")
	raw, err := command.Output()
	tree := strings.TrimSpace(string(raw))
	if err != nil || !validDomesticReleaseSHA(tree) {
		return "", errors.New("installed Survey source web tree cannot be verified")
	}
	return tree, nil
}

// A backend incremental package may reuse an unchanged Host. It must preserve
// both provenances instead of rewriting the manifest to pretend a frontend
// build occurred. Git tree equality and every released asset are verified.
func validateInstalledSurveyPackage(in testconfig.InstalledSurveyInputs, webTree func(string, string) (string, error)) (installedSurveyProvenance, error) {
	var out installedSurveyProvenance
	if !filepath.IsAbs(in.Binary) || !filepath.IsAbs(in.Root) || !validDomesticReleaseSHA(in.SHA) || len(in.BinarySHA256) != 64 || webTree == nil {
		return out, errors.New("invalid installed Survey package inputs")
	}
	raw, err := os.ReadFile(in.Binary)
	if err != nil {
		return out, errors.New("installed Survey binary is unreadable")
	}
	binaryDigest := sha256.Sum256(raw)
	if hex.EncodeToString(binaryDigest[:]) != in.BinarySHA256 {
		return out, errors.New("installed Survey binary digest mismatch")
	}
	hostSHA := in.HostSHA
	if hostSHA == "" {
		hostSHA = in.SHA // Preserve full-build callers' original contract.
	}
	if !validDomesticReleaseSHA(hostSHA) || hostSHA != in.SHA && (len(in.HostManifestSHA256) != 64 || in.SourceRepository == "") {
		return out, errors.New("independent installed Host requires explicit source and manifest provenance")
	}
	root, err := os.OpenRoot(filepath.Join(in.Root, "web", "dist"))
	if err != nil {
		return out, errors.New("installed Host root is unreadable")
	}
	defer root.Close()
	raw, err = root.ReadFile("asset-manifest.json")
	var manifest struct {
		SourceSHA    string `json:"source_sha"`
		ReleaseFiles map[string]struct {
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		} `json:"release_files"`
	}
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.SourceSHA != hostSHA || len(manifest.ReleaseFiles) == 0 {
		return out, errors.New("installed Host manifest provenance or inventory mismatch")
	}
	manifestDigest := sha256.Sum256(raw)
	out.HostSHA, out.ManifestSHA256 = hostSHA, hex.EncodeToString(manifestDigest[:])
	if in.HostManifestSHA256 != "" && out.ManifestSHA256 != in.HostManifestSHA256 {
		return out, errors.New("installed Host manifest digest mismatch")
	}
	out.AppWebTree, err = webTree(in.SourceRepository, in.SHA)
	if err != nil {
		return out, err
	}
	out.HostWebTree, err = webTree(in.SourceRepository, hostSHA)
	if err != nil || !validDomesticReleaseSHA(out.AppWebTree) || out.AppWebTree != out.HostWebTree {
		return out, errors.New("installed app and reused Host source web trees differ")
	}
	paths := make([]string, 0, len(manifest.ReleaseFiles))
	for path := range manifest.ReleaseFiles {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	assets := sha256.New()
	for _, path := range paths {
		if !fs.ValidPath(path) || strings.Contains(path, "\\") {
			return out, errors.New("installed Host inventory escapes its resource root")
		}
		info := manifest.ReleaseFiles[path]
		content, e := root.ReadFile(path)
		digest := sha256.Sum256(content)
		if e != nil || int64(len(content)) != info.Bytes || hex.EncodeToString(digest[:]) != info.SHA256 {
			return out, errors.New("installed Host asset digest mismatch")
		}
		assets.Write([]byte(path + "\x00" + info.SHA256 + "\n"))
	}
	out.AssetSHA256 = hex.EncodeToString(assets.Sum(nil))
	return out, nil
}

func TestInstalledSurveyPackageProvenanceAllowsOnlyUnchangedHost(t *testing.T) {
	for _, kind := range []string{"full_build", "unchanged_reused_host", "missing_provenance", "wrong_binary", "wrong_manifest_source", "wrong_manifest_digest", "changed_web_tree", "missing_source_object", "asset_drift", "missing_asset", "escaping_asset"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			dist := filepath.Join(root, "web", "dist")
			if err := os.MkdirAll(dist, 0700); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(root, "aicrm")
			if err := os.WriteFile(binary, []byte("synthetic app bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			content := []byte("synthetic unchanged Host")
			if err := os.WriteFile(filepath.Join(dist, "index.html"), content, 0600); err != nil {
				t.Fatal(err)
			}
			appSHA, hostSHA, tree := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
			if kind == "full_build" {
				hostSHA = appSHA
			}
			assetDigest := sha256.Sum256(content)
			manifest := map[string]any{"source_sha": hostSHA, "release_files": map[string]any{"index.html": map[string]any{"sha256": hex.EncodeToString(assetDigest[:]), "bytes": len(content)}}}
			if kind == "wrong_manifest_source" {
				manifest["source_sha"] = strings.Repeat("d", 40)
			}
			if kind == "escaping_asset" {
				manifest["release_files"] = map[string]any{"../private": map[string]any{"sha256": hex.EncodeToString(assetDigest[:]), "bytes": len(content)}}
			}
			raw, _ := json.Marshal(manifest)
			if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			binaryDigest := sha256.Sum256([]byte("synthetic app bytes"))
			manifestDigest := sha256.Sum256(raw)
			in := testconfig.InstalledSurveyInputs{Binary: binary, Root: root, SHA: appSHA, BinarySHA256: hex.EncodeToString(binaryDigest[:]), HostSHA: hostSHA, HostManifestSHA256: hex.EncodeToString(manifestDigest[:]), SourceRepository: root}
			switch kind {
			case "full_build":
				in.HostSHA, in.HostManifestSHA256, in.SourceRepository = "", "", ""
			case "missing_provenance":
				in.HostManifestSHA256 = ""
			case "wrong_binary":
				in.BinarySHA256 = strings.Repeat("d", 64)
			case "wrong_manifest_digest":
				in.HostManifestSHA256 = strings.Repeat("d", 64)
			case "asset_drift":
				if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing_asset":
				if err := os.Remove(filepath.Join(dist, "index.html")); err != nil {
					t.Fatal(err)
				}
			}
			readTree := func(_ string, sha string) (string, error) {
				if kind == "missing_source_object" {
					return "", errors.New("source unavailable")
				}
				if kind == "changed_web_tree" && sha == hostSHA {
					return strings.Repeat("d", 40), nil
				}
				return tree, nil
			}
			out, err := validateInstalledSurveyPackage(in, readTree)
			if kind == "full_build" || kind == "unchanged_reused_host" {
				if err != nil || out.HostSHA != hostSHA || out.AppWebTree != tree || out.HostWebTree != tree || len(out.AssetSHA256) != 64 {
					t.Fatalf("legal package provenance was rejected: %+v err=%v", out, err)
				}
			} else if err == nil {
				t.Fatalf("%s was accepted as an installed package", kind)
			}
		})
	}
}

func TestInstalledSurveyWebTreeVerifiesActualGitObjects(t *testing.T) {
	repository := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		base := []string{"-C", repository, "-c", "user.name=fixture", "-c", "user.email=fixture@invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}
		command := exec.Command("git", append(base, args...)...)
		raw, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("synthetic Git fixture command failed: %v", err)
		}
		return strings.TrimSpace(string(raw))
	}
	git("init", "--quiet")
	if err := os.Mkdir(filepath.Join(repository, "web"), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repository, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("web/index.html", "original Host")
	git("add", ".")
	git("commit", "--quiet", "-m", "synthetic Host")
	host := git("rev-parse", "HEAD")
	write("backend.txt", "backend change only")
	git("add", ".")
	git("commit", "--quiet", "-m", "synthetic backend")
	app := git("rev-parse", "HEAD")
	hostTree, err := installedSurveyWebTree(repository, host)
	if err != nil {
		t.Fatal(err)
	}
	appTree, err := installedSurveyWebTree(repository, app)
	if err != nil || appTree != hostTree {
		t.Fatal("equal web objects were not proven across backend-only commits", err)
	}
	write("web/index.html", "changed Host")
	git("add", ".")
	git("commit", "--quiet", "-m", "synthetic frontend")
	changed, err := installedSurveyWebTree(repository, git("rev-parse", "HEAD"))
	if err != nil || changed == hostTree {
		t.Fatal("actual changed web object was accepted as unchanged", err)
	}
	if _, err = installedSurveyWebTree(repository, strings.Repeat("0", 40)); err == nil {
		t.Fatal("unavailable source object was guessed")
	}
}
