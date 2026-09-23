// Command migrate-hxc-daily-lessons freezes the public HXC daily-lesson
// catalog and imports only UUID lessons into V3 Media. Apply never calls the
// source service; it consumes a verified offline snapshot directory.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const (
	defaultSourceBase = "https://ip.lhbl.com.cn"
	lessonAppID       = "wx0ca836834b18e989"
	manifestFile      = "manifest.json"
)

var (
	errInvalidArguments = errors.New("invalid arguments")
	errInvalidSnapshot  = errors.New("invalid snapshot")
	uuidPattern         = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type options struct {
	mode, snapshotDir, manifestSHA, sourceBase            string
	actor, expectedCount, expectedTotal, expectedExcluded int
	confirm                                               bool
}

type sourceLesson struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	PublishDate string `json:"publish_date"`
}

type sourceList struct {
	Lessons []sourceLesson `json:"lessons"`
	Total   int            `json:"total"`
	HasMore bool           `json:"has_more"`
}

type manifest struct {
	SchemaVersion   int       `json:"schema_version"`
	SourceSystem    string    `json:"source_system"`
	SourceBase      string    `json:"source_base"`
	SourceVersion   string    `json:"source_version"`
	CapturedAt      time.Time `json:"captured_at"`
	SourceTotal     int       `json:"source_total"`
	ExcludedNonUUID int       `json:"excluded_non_uuid"`
	Records         []record  `json:"records"`
}

type record struct {
	ID                 string `json:"id"`
	Title              string `json:"title"`
	PublishDate        string `json:"publish_date"`
	AppID              string `json:"app_id"`
	PagePath           string `json:"page_path"`
	CoverFile          string `json:"cover_file"`
	CoverSHA256        string `json:"cover_sha256"`
	CoverSize          int64  `json:"cover_size"`
	Width              int32  `json:"width"`
	Height             int32  `json:"height"`
	SourceRecordDigest string `json:"source_record_digest"`
}

type recordFacts struct {
	ID, Title, PublishDate, AppID, PagePath, CoverSHA256 string
	CoverSize                                            int64
	Width, Height                                        int32
}

type report struct {
	Mode            string `json:"mode"`
	ManifestSHA256  string `json:"manifest_sha256"`
	Expected        int    `json:"expected"`
	Count           int    `json:"count"`
	New             int    `json:"new"`
	Replayed        int    `json:"replayed"`
	Verified        int    `json:"verified"`
	ExcludedNonUUID int    `json:"excluded_non_uuid"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, publicError(err))
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-hxc-daily-lessons", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var cfg options
	flags.StringVar(&cfg.mode, "mode", "inspect", "extract|inspect|dry-run|apply|verify|sync")
	flags.StringVar(&cfg.snapshotDir, "snapshot-dir", "", "0600 offline snapshot directory")
	flags.StringVar(&cfg.manifestSHA, "manifest-sha256", "", "exact manifest sha256 required outside inspect/extract")
	flags.StringVar(&cfg.sourceBase, "source-base-url", defaultSourceBase, "fixed HXC public source for extract")
	flags.IntVar(&cfg.actor, "actor-admin-user-id", 0, "explicit administrator authorizing import")
	flags.IntVar(&cfg.expectedCount, "expected-count", 1062, "exact UUID record count")
	flags.IntVar(&cfg.expectedTotal, "expected-source-total", 1162, "exact public source count for extract")
	flags.IntVar(&cfg.expectedExcluded, "expected-excluded-non-uuid", 100, "exact non-UUID exclusion count for extract")
	flags.BoolVar(&cfg.confirm, "confirm-apply", false, "confirm exact snapshot writes")
	if err := flags.Parse(args); err != nil || cfg.expectedCount < 1 {
		return errInvalidArguments
	}
	if cfg.mode == "extract" {
		if cfg.snapshotDir == "" {
			return errInvalidArguments
		}
		return extract(ctx, cfg)
	}
	if cfg.mode == "sync" {
		return syncIncremental(ctx, cfg)
	}
	if cfg.snapshotDir == "" {
		return errInvalidArguments
	}
	snapshot, raw, err := loadSnapshot(cfg.snapshotDir, cfg.expectedCount)
	if err != nil {
		return err
	}
	digest := hexDigest(raw)
	out := report{Mode: cfg.mode, ManifestSHA256: digest, Expected: cfg.expectedCount, Count: len(snapshot.Records), ExcludedNonUUID: snapshot.ExcludedNonUUID}
	if cfg.mode == "inspect" {
		return printJSON(out)
	}
	if cfg.mode != "dry-run" && cfg.mode != "apply" && cfg.mode != "verify" || cfg.actor < 1 || !constantDigest(cfg.manifestSHA, digest) || cfg.mode == "apply" && !cfg.confirm {
		return errInvalidArguments
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 4, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	repository, err := mediastore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	for _, item := range snapshot.Records {
		png, readErr := os.ReadFile(filepath.Join(cfg.snapshotDir, filepath.FromSlash(item.CoverFile)))
		if readErr != nil || int64(len(png)) != item.CoverSize || hexDigest(png) != item.CoverSHA256 {
			return errInvalidSnapshot
		}
		input := mediastore.HXCDailyLessonImport{SourceID: item.ID, Title: item.Title, AppID: item.AppID, PagePath: item.PagePath, PNG: png, Width: item.Width, Height: item.Height, Actor: int64(cfg.actor), IdempotencyKey: "hxc-daily-lesson:" + item.ID, SourceRecordDigest: "sha256:" + item.SourceRecordDigest}
		var result mediastore.HXCDailyLessonImportResult
		err = uow.Within(ctx, func(tx context.Context) error {
			var importErr error
			if cfg.mode == "apply" {
				result, importErr = repository.ImportHXCDailyLessonWithin(tx, input)
			} else {
				result, importErr = repository.InspectHXCDailyLessonWithin(tx, input)
			}
			return importErr
		})
		if err != nil {
			return err
		}
		if cfg.mode == "verify" && !result.Replayed {
			return errors.New("required HXC daily lesson mapping is missing")
		}
		if result.Replayed {
			out.Replayed++
		} else {
			out.New++
		}
		if cfg.mode == "verify" {
			out.Verified++
		}
	}
	if cfg.mode == "dry-run" {
		out.Verified = len(snapshot.Records)
	}
	return printJSON(out)
}

// syncIncremental performs a lightweight catalog scan and imports only UUIDs
// that have no complete immutable mapping yet. The public source has no
// cursor or updated_at, so existing mappings are deliberately not overwritten.
func syncIncremental(ctx context.Context, cfg options) error {
	if cfg.sourceBase != defaultSourceBase || cfg.actor < 1 {
		return errInvalidArguments
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	lessons, total, excluded, err := readSourceCatalog(ctx, client)
	if err != nil {
		return err
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 4, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	repository, err := mediastore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	tempDir, err := os.MkdirTemp("", "hxc-daily-lessons-sync-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	out := report{Mode: "sync", Count: len(lessons), Expected: total, ExcludedNonUUID: excluded}
	for _, lesson := range lessons {
		mapped, err := hasCompleteHXCDailyLessonMapping(ctx, uow, repository, lesson.ID)
		if err != nil {
			return err
		}
		if mapped {
			out.Replayed++
			continue
		}
		png, getErr := get(ctx, client, defaultSourceBase+"/api/share/lesson-card/"+url.PathEscape(lesson.ID)+".png")
		if getErr != nil {
			return getErr
		}
		inspection, inspectErr := domain.Inspect("lesson-card.png", "image/png", png)
		if inspectErr != nil {
			return errInvalidSnapshot
		}
		coverPath := filepath.Join(tempDir, lesson.ID+".png")
		if err := os.WriteFile(coverPath, png, 0o600); err != nil {
			return err
		}
		item := record{ID: lesson.ID, Title: strings.TrimSpace(lesson.Title), PublishDate: lesson.PublishDate, AppID: lessonAppID, PagePath: "pages/article/article?lesson_id=" + lesson.ID + "&from=learn", CoverSHA256: hexDigest(png), CoverSize: int64(len(png)), Width: inspection.Width, Height: inspection.Height}
		item.SourceRecordDigest = sourceRecordDigest(item)
		input := mediastore.HXCDailyLessonImport{SourceID: item.ID, Title: item.Title, AppID: item.AppID, PagePath: item.PagePath, PNG: png, Width: item.Width, Height: item.Height, Actor: int64(cfg.actor), IdempotencyKey: "hxc-daily-lesson:" + item.ID, SourceRecordDigest: "sha256:" + item.SourceRecordDigest}
		if err := uow.Within(ctx, func(tx context.Context) error {
			_, importErr := repository.ImportHXCDailyLessonWithin(tx, input)
			return importErr
		}); err != nil {
			return err
		}
		out.New++
	}
	return printJSON(out)
}

func hasCompleteHXCDailyLessonMapping(ctx context.Context, uow *platformpostgres.UnitOfWork, repository *mediastore.Repository, lessonID string) (bool, error) {
	var imageFound, miniFound bool
	err := uow.Within(ctx, func(tx context.Context) error {
		var err error
		_, imageFound, err = repository.ResolveLegacyMaterialMapping(tx, mediaport.LegacyMaterialReference{SourceSystem: mediastore.HXCDailyLessonSourceSystem, MaterialKind: "image", LegacyID: lessonID})
		if err != nil {
			return err
		}
		_, miniFound, err = repository.ResolveLegacyMaterialMapping(tx, mediaport.LegacyMaterialReference{SourceSystem: mediastore.HXCDailyLessonSourceSystem, MaterialKind: "miniprogram", LegacyID: lessonID})
		return err
	})
	if err != nil {
		return false, err
	}
	if imageFound != miniFound {
		return false, mediastore.ErrConflict
	}
	return imageFound, nil
}

func extract(ctx context.Context, cfg options) error {
	if cfg.sourceBase != defaultSourceBase || cfg.expectedTotal < 1 || cfg.expectedExcluded < 0 {
		return errInvalidArguments
	}
	if info, err := os.Stat(cfg.snapshotDir); err == nil && info.IsDir() {
		entries, readErr := os.ReadDir(cfg.snapshotDir)
		if readErr != nil || len(entries) != 0 {
			return errInvalidArguments
		}
	} else if !os.IsNotExist(err) {
		return errInvalidArguments
	}
	if err := os.MkdirAll(filepath.Join(cfg.snapshotDir, "covers"), 0o700); err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	lessons, total, excluded, err := readSourceCatalog(ctx, client)
	if err != nil || total != cfg.expectedTotal || excluded != cfg.expectedExcluded || len(lessons) != cfg.expectedCount {
		return errInvalidSnapshot
	}
	snapshot := manifest{SchemaVersion: 1, SourceSystem: mediastore.HXCDailyLessonSourceSystem, SourceBase: defaultSourceBase, CapturedAt: time.Now().UTC(), SourceTotal: total, ExcludedNonUUID: excluded, Records: make([]record, 0, len(lessons))}
	if response, healthErr := get(ctx, client, defaultSourceBase+"/health"); healthErr == nil {
		var health struct {
			Version string `json:"version"`
		}
		_ = json.Unmarshal(response, &health)
		snapshot.SourceVersion = health.Version
	}
	for _, lesson := range lessons {
		coverURL := defaultSourceBase + "/api/share/lesson-card/" + url.PathEscape(lesson.ID) + ".png"
		png, getErr := get(ctx, client, coverURL)
		if getErr != nil {
			return getErr
		}
		inspection, inspectErr := domain.Inspect("lesson-card.png", "image/png", png)
		if inspectErr != nil {
			return errInvalidSnapshot
		}
		coverFile := "covers/" + lesson.ID + ".png"
		if err = os.WriteFile(filepath.Join(cfg.snapshotDir, filepath.FromSlash(coverFile)), png, 0o600); err != nil {
			return err
		}
		item := record{ID: lesson.ID, Title: strings.TrimSpace(lesson.Title), PublishDate: lesson.PublishDate, AppID: lessonAppID, PagePath: "pages/article/article?lesson_id=" + lesson.ID + "&from=learn", CoverFile: coverFile, CoverSHA256: hexDigest(png), CoverSize: int64(len(png)), Width: inspection.Width, Height: inspection.Height}
		item.SourceRecordDigest = sourceRecordDigest(item)
		snapshot.Records = append(snapshot.Records, item)
	}
	sort.Slice(snapshot.Records, func(i, j int) bool { return snapshot.Records[i].ID < snapshot.Records[j].ID })
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err = os.WriteFile(filepath.Join(cfg.snapshotDir, manifestFile), raw, 0o600); err != nil {
		return err
	}
	return printJSON(report{Mode: "extract", ManifestSHA256: hexDigest(raw), Expected: cfg.expectedCount, Count: len(snapshot.Records), ExcludedNonUUID: excluded})
}

func readSourceCatalog(ctx context.Context, client *http.Client) ([]sourceLesson, int, int, error) {
	all := make([]sourceLesson, 0)
	total := -1
	for offset := 0; ; offset += 50 {
		raw, err := get(ctx, client, fmt.Sprintf("%s/api/lesson/list?offset=%d&limit=50", defaultSourceBase, offset))
		if err != nil {
			return nil, 0, 0, err
		}
		var page sourceList
		if json.Unmarshal(raw, &page) != nil || page.Total < 1 || total >= 0 && page.Total != total {
			return nil, 0, 0, errInvalidSnapshot
		}
		total = page.Total
		all = append(all, page.Lessons...)
		if !page.HasMore {
			break
		}
	}
	seen := make(map[string]struct{}, len(all))
	eligible := make([]sourceLesson, 0, len(all))
	excluded := 0
	for _, item := range all {
		if _, exists := seen[item.ID]; exists || item.ID == "" {
			return nil, 0, 0, errInvalidSnapshot
		}
		seen[item.ID] = struct{}{}
		if !uuidPattern.MatchString(item.ID) {
			excluded++
			continue
		}
		if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Title) != item.Title || len(item.Title) > 200 || item.PublishDate == "" {
			return nil, 0, 0, errInvalidSnapshot
		}
		eligible = append(eligible, item)
	}
	if len(all) != total {
		return nil, 0, 0, errInvalidSnapshot
	}
	return eligible, total, excluded, nil
}

func loadSnapshot(directory string, expected int) (manifest, []byte, error) {
	raw, err := os.ReadFile(filepath.Join(directory, manifestFile))
	if err != nil {
		return manifest{}, nil, errInvalidSnapshot
	}
	var snapshot manifest
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.SchemaVersion != 1 || snapshot.SourceSystem != mediastore.HXCDailyLessonSourceSystem || snapshot.SourceBase != defaultSourceBase || snapshot.CapturedAt.IsZero() || len(snapshot.Records) != expected || snapshot.ExcludedNonUUID < 0 {
		return manifest{}, nil, errInvalidSnapshot
	}
	seen := make(map[string]struct{}, len(snapshot.Records))
	for _, item := range snapshot.Records {
		if !uuidPattern.MatchString(item.ID) || item.Title == "" || item.AppID != lessonAppID || item.PagePath != "pages/article/article?lesson_id="+item.ID+"&from=learn" || item.CoverFile != "covers/"+item.ID+".png" || item.CoverSize < 1 || item.Width < 1 || item.Height < 1 || !validHexDigest(item.CoverSHA256) || item.SourceRecordDigest != sourceRecordDigest(item) {
			return manifest{}, nil, errInvalidSnapshot
		}
		if _, exists := seen[item.ID]; exists {
			return manifest{}, nil, errInvalidSnapshot
		}
		seen[item.ID] = struct{}{}
	}
	return snapshot, raw, nil
}

func sourceRecordDigest(item record) string {
	raw, _ := json.Marshal(recordFacts{ID: item.ID, Title: item.Title, PublishDate: item.PublishDate, AppID: item.AppID, PagePath: item.PagePath, CoverSHA256: item.CoverSHA256, CoverSize: item.CoverSize, Width: item.Width, Height: item.Height})
	return hexDigest(raw)
}

func get(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > domain.MaxImageBytes && strings.Contains(address, "/lesson-card/") {
		return nil, errInvalidSnapshot
	}
	limit := int64(4 << 20)
	if strings.Contains(address, "/lesson-card/") {
		limit = domain.MaxImageBytes + 1
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit))
	if err != nil || int64(len(raw)) >= limit {
		return nil, errInvalidSnapshot
	}
	return raw, nil
}

func hexDigest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func validHexDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}
func constantDigest(expected, actual string) bool {
	left, e1 := hex.DecodeString(expected)
	right, e2 := hex.DecodeString(actual)
	return e1 == nil && e2 == nil && len(left) == sha256.Size && len(right) == sha256.Size && subtle.ConstantTimeCompare(left, right) == 1
}
func printJSON(value any) error { return json.NewEncoder(os.Stdout).Encode(value) }
func publicError(err error) string {
	switch {
	case errors.Is(err, errInvalidArguments):
		return "hxc daily lesson migration failed: invalid_arguments"
	case errors.Is(err, errInvalidSnapshot):
		return "hxc daily lesson migration failed: invalid_snapshot"
	default:
		return "hxc daily lesson migration failed: unavailable"
	}
}
