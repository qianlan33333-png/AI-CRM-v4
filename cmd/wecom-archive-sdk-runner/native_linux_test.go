//go:build linux && cgo

package main

import (
	"context"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/archivesdk"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureC = `
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
typedef struct {char*b;int n;} Slice_t; typedef struct {char*i;int in;char*d;int dn;int done;} MediaData_t; typedef struct{} WeWorkFinanceSdk_t; static int live=0, decrypts=0;
__attribute__((destructor)) static void done(){const char*p=getenv("ARCHIVE_FIXTURE_STATS");if(p){FILE*f=fopen(p,"a");if(f){fprintf(f,"live=%d decrypts=%d\\n",live,decrypts);fclose(f);}}}
WeWorkFinanceSdk_t* NewSdk(){live++;return calloc(1,sizeof(WeWorkFinanceSdk_t));} void DestroySdk(WeWorkFinanceSdk_t*s){if(s){live--;free(s);}} int Init(WeWorkFinanceSdk_t*s,const char*c,const char*x){return s&&c&&x?0:1;}
Slice_t* NewSlice(){live++;return calloc(1,sizeof(Slice_t));} void FreeSlice(Slice_t*s){if(s){live--;free(s);}} char* GetContentFromSlice(Slice_t*s){return s->b;} int GetSliceLen(Slice_t*s){return s->n;} static int put(Slice_t*s,const char*x){s->n=strlen(x);s->b=(char*)x;return 0;}
int GetChatData(WeWorkFinanceSdk_t*s,unsigned long long q,unsigned int l,const char*p,const char*x,int t,Slice_t*out){printf("vendor-noise");fflush(stdout);return put(out,"{\\\"errcode\\\":0,\\\"chatdata\\\":[]}");} int DecryptData(const char*k,const char*m,Slice_t*out){decrypts++;return put(out,"{}");}
MediaData_t* NewMediaData(){live++;return calloc(1,sizeof(MediaData_t));}void FreeMediaData(MediaData_t*m){if(m){live--;free(m);}}int GetMediaData(WeWorkFinanceSdk_t*s,const char*i,const char*f,const char*p,const char*x,int t,MediaData_t*m){m->d="x";m->dn=1;m->i="";m->in=0;m->done=1;return 0;}char* GetData(MediaData_t*m){return m->d;}int GetDataLen(MediaData_t*m){return m->dn;}char* GetOutIndexBuf(MediaData_t*m){return m->i;}int GetIndexLen(MediaData_t*m){return m->in;}int IsMediaDataFinish(MediaData_t*m){return m->done;}
`

func TestRunnerKeepsVendorStdoutOutOfFramesAndFreesHandles(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("C compiler unavailable")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "fixture.c")
	lib := filepath.Join(dir, "fixture.so")
	stats := filepath.Join(dir, "stats")
	runnerStats := filepath.Join(dir, "runner-stats")
	if err := os.WriteFile(source, []byte(fixtureC), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cc", "-shared", "-fPIC", source, "-o", lib).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	runner := filepath.Join(dir, "runner")
	if out, err := exec.Command("go", "build", "-o", runner, ".").CombinedOutput(); err != nil {
		t.Fatalf("runner: %v %s", err, out)
	}
	t.Setenv("ARCHIVE_FIXTURE_STATS", stats)
	t.Setenv("ARCHIVE_RUNNER_ALLOC_STATS", runnerStats)
	response, err := archivesdk.Call(context.Background(), runner, archivesdk.Request{Operation: "fetch", LibraryPath: lib, CorpID: "corp", Secret: "secret", Limit: 1})
	if err != nil || response.ErrorCode != "" || !strings.Contains(string(response.Data), "errcode") {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	response, err = archivesdk.Call(context.Background(), runner, archivesdk.Request{Operation: "decrypt_batch", LibraryPath: lib, DecryptItems: []archivesdk.DecryptItem{{DecryptKey: "a", EncryptedMessage: "b"}, {DecryptKey: "c", EncryptedMessage: "d"}, {DecryptKey: "e", EncryptedMessage: "f"}}})
	if err != nil || len(response.Items) != 3 {
		t.Fatalf("batch=%+v err=%v", response, err)
	}
	raw, err := os.ReadFile(stats)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "live=1") {
		t.Fatalf("leaked vendor handle: %s", raw)
	}
	if !strings.Contains(string(raw), "decrypts=3") {
		t.Fatalf("batch did not use one SDK load: %s", raw)
	}
	raw, err = os.ReadFile(runnerStats)
	if err != nil || strings.Contains(string(raw), "runner_allocations=1") || !strings.Contains(string(raw), "runner_allocations=0") {
		t.Fatalf("runner output allocation leak stats=%q err=%v", raw, err)
	}
}
