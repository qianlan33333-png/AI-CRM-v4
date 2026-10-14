package main

import (
	"bytes"
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
	HostSHA, ManifestSHA256, AssetSHA256, AppFrontendInputs, HostFrontendInputs string
}

func installedFrontendRegistry(path string) bool {
	return path == "web/donor-sources/source-index.json" || path == "web/donor-sources/source-lock.json"
}

// The legacy helper name remains for fixture consumers. It fingerprints the
// real Git type/mode/blob/path inventory, excluding only the two registries
// already excluded by the release builder; this is not a fabricated Git tree.
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
	command = exec.CommandContext(ctx, "git", "-C", repository, "cat-file", "-t", sha+":web")
	raw, err = command.Output()
	if err != nil || strings.TrimSpace(string(raw)) != "tree" {
		return "", errors.New("installed Survey web source is not a Git tree")
	}
	command = exec.CommandContext(ctx, "git", "-C", repository, "ls-tree", "-r", "-z", "--full-tree", sha, "--", "web")
	raw, err = command.Output()
	if err != nil {
		return "", errors.New("installed Survey frontend inputs cannot be verified")
	}
	digest := sha256.New()
	digest.Write([]byte("aicrm.frontend-inputs.v1\x00"))
	for _, entry := range bytes.Split(raw, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		_, path, ok := bytes.Cut(entry, []byte{'\t'})
		if !ok {
			return "", errors.New("invalid installed Survey Git input inventory")
		}
		if installedFrontendRegistry(string(path)) {
			continue
		}
		digest.Write(entry)
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// A backend incremental package may reuse an unchanged Host. It must preserve
// both provenances instead of rewriting the manifest to pretend a frontend
// build occurred. Actual frontend input equality and every released asset are verified.
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
	out.AppFrontendInputs, err = webTree(in.SourceRepository, in.SHA)
	if err != nil {
		return out, err
	}
	out.HostFrontendInputs, err = webTree(in.SourceRepository, hostSHA)
	inputDigest, digestErr := hex.DecodeString(out.AppFrontendInputs)
	if err != nil || digestErr != nil || len(inputDigest) != sha256.Size || out.AppFrontendInputs != out.HostFrontendInputs {
		return out, errors.New("installed app and reused Host frontend inputs differ")
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
	for _, kind := range []string{"full_build", "unchanged_reused_host", "missing_provenance", "wrong_binary", "wrong_manifest_source", "wrong_manifest_digest", "changed_web_tree", "invalid_input_digest", "missing_source_object", "asset_drift", "missing_asset", "escaping_asset"} {
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
			appSHA, hostSHA, tree := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 64)
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
				if kind == "invalid_input_digest" {
					return "not-a-source-fingerprint", nil
				}
				if kind == "missing_source_object" {
					return "", errors.New("source unavailable")
				}
				if kind == "changed_web_tree" && sha == hostSHA {
					return strings.Repeat("d", 64), nil
				}
				return tree, nil
			}
			out, err := validateInstalledSurveyPackage(in, readTree)
			if kind == "full_build" || kind == "unchanged_reused_host" {
				if err != nil || out.HostSHA != hostSHA || out.AppFrontendInputs != tree || out.HostFrontendInputs != tree || len(out.AssetSHA256) != 64 {
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
		base := []string{"-C", repository, "-c", "user.name=fixture", "-c", "user.email=fixture@invalid", "-c", "commit.gpgsign=false", "-c", "core.filemode=true", "-c", "core.hooksPath=/dev/null"}
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
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repository, name)), 0700); err != nil {
			t.Fatal(err)
		}
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
	write("web/donor-sources/source-index.json", "application registry revision")
	write("web/donor-sources/source-lock.json", "application registry lock revision")
	git("add", ".")
	git("commit", "--quiet", "-m", "synthetic application registries")
	metadata := git("rev-parse", "HEAD")
	metadataInputs, err := installedSurveyWebTree(repository, metadata)
	if err != nil || metadataInputs != hostTree {
		t.Fatal("registry-only change rejected reused Host", err)
	}
	_, sourceFile, _, _ := runtime.Caller(0)
	scripts := filepath.Join(filepath.Dir(sourceFile), "..", "..", "scripts")
	python := exec.Command("python3", "-c", "import sys;from pathlib import Path;sys.path.insert(0,sys.argv[1]);import domestic_main_release as m;print(m._host_frontend_inputs(Path(sys.argv[2]),sys.argv[3]))", scripts, filepath.Join(repository, ".git"), metadata)
	controllerDigest, err := python.Output()
	if err != nil || strings.TrimSpace(string(controllerDigest)) != metadataInputs {
		t.Fatal("controller and Go fixture fingerprint different actual inputs", err)
	}
	for _, change := range []string{"code", "lock", "unknown_registry", "delete", "rename", "mode", "chinese", "quote", "newline", "root_blob"} {
		t.Run(change, func(t *testing.T) {
			git("reset", "--hard", "--quiet", metadata)
			switch change {
			case "code":
				write("web/index.html", "changed Host")
			case "lock":
				write("web/package-lock.json", "changed dependency inputs")
			case "unknown_registry":
				write("web/donor-sources/source-index.json.extra", "unregistered input")
			case "delete":
				if err := os.Remove(filepath.Join(repository, "web/index.html")); err != nil {
					t.Fatal(err)
				}
			case "rename":
				if err := os.Rename(filepath.Join(repository, "web/index.html"), filepath.Join(repository, "web/renamed.html")); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(filepath.Join(repository, "web/index.html"), 0700); err != nil {
					t.Fatal(err)
				}
			case "chinese":
				write("web/中文.ts", "changed Chinese input")
			case "quote":
				write("web/quoted\".ts", "changed quoted input")
			case "newline":
				write("web/newline\n.ts", "changed newline input")
			case "root_blob":
				if err := os.RemoveAll(filepath.Join(repository, "web")); err != nil {
					t.Fatal(err)
				}
				write("web", "not a directory")
			}
			git("add", "-A")
			git("commit", "--quiet", "-m", "synthetic changed frontend input")
			changed, err := installedSurveyWebTree(repository, git("rev-parse", "HEAD"))
			if change == "root_blob" {
				if err == nil {
					t.Fatal("web root shape change accepted")
				}
				return
			}
			if err != nil || changed == hostTree {
				t.Fatal("changed frontend input accepted", change, err)
			}
		})
	}
	if _, err = installedSurveyWebTree(repository, strings.Repeat("0", 40)); err == nil {
		t.Fatal("unavailable source object was guessed")
	}
	git("reset", "--hard", "--quiet", metadata)
	git("rm", "-r", "--quiet", "web")
	git("commit", "--quiet", "-m", "synthetic missing web")
	if _, err = installedSurveyWebTree(repository, git("rev-parse", "HEAD")); err == nil {
		t.Fatal("missing web object was guessed")
	}
}

func TestInstalledFrontendRegistryPolicyMatchesBuilder(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	repository := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "-c", "import sys,json;sys.path.insert(0,sys.argv[1]);import domestic_release_build as b;print(json.dumps(sorted(b.APPLICATION_SOURCE_REGISTRIES)))", filepath.Join(repository, "scripts"))
	raw, err := command.Output()
	if err != nil {
		t.Fatal("release builder registry policy unavailable", err)
	}
	var paths []string
	if err := json.Unmarshal(raw, &paths); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatal("release builder exclusions changed; review both guards", paths)
	}
	for _, path := range paths {
		if !installedFrontendRegistry(path) {
			t.Fatal("fixture differs from actual builder", path)
		}
	}
	for _, path := range []string{"web/donor-sources/source-index.json.extra", "web/donor-sources/source-lock.json/bundle.js", "web/v3/app.ts", "web/package-lock.json"} {
		if installedFrontendRegistry(path) {
			t.Fatal("unregistered input excluded", path)
		}
	}
}
