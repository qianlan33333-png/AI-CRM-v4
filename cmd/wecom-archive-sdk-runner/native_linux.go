//go:build linux && cgo

package main

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#define ARCHIVE_MAX_BYTES (8*1024*1024)
#define ARCHIVE_MAX_RESPONSE_BYTES (ARCHIVE_MAX_BYTES/2)
typedef void Sdk; typedef void Slice; typedef void Media;
typedef Sdk* (*new_sdk_t)(void); typedef void (*destroy_sdk_t)(Sdk*); typedef int (*init_t)(Sdk*,const char*,const char*);
typedef int (*chat_t)(Sdk*,unsigned long long,unsigned int,const char*,const char*,int,Slice*); typedef int (*decrypt_t)(const char*,const char*,Slice*);
typedef Slice* (*new_slice_t)(void); typedef void (*free_slice_t)(Slice*); typedef char* (*slice_content_t)(Slice*); typedef int (*slice_len_t)(Slice*);
typedef int (*media_t)(Sdk*,const char*,const char*,const char*,const char*,int,Media*); typedef Media* (*new_media_t)(void); typedef void (*free_media_t)(Media*); typedef char* (*media_data_t)(Media*); typedef int (*media_len_t)(Media*); typedef char* (*media_index_t)(Media*); typedef int (*media_index_len_t)(Media*); typedef int (*media_finished_t)(Media*);
typedef struct {void*h;new_sdk_t NewSdk;destroy_sdk_t DestroySdk;init_t Init;chat_t GetChatData;decrypt_t DecryptData;new_slice_t NewSlice;free_slice_t FreeSlice;slice_content_t GetContentFromSlice;slice_len_t GetSliceLen;media_t GetMediaData;new_media_t NewMediaData;free_media_t FreeMediaData;media_data_t GetData;media_len_t GetDataLen;media_index_t GetOutIndexBuf;media_index_len_t GetIndexLen;media_finished_t IsMediaDataFinish;} api;
static int load(const char*path,api*a){memset(a,0,sizeof(*a));a->h=dlopen(path,RTLD_NOW|RTLD_LOCAL);if(!a->h)return -1;
#define F(n) a->n=(void*)dlsym(a->h,#n);if(!a->n){dlclose(a->h);return -2;}
F(NewSdk)F(DestroySdk)F(Init)F(GetChatData)F(DecryptData)F(NewSlice)F(FreeSlice)F(GetContentFromSlice)F(GetSliceLen)F(GetMediaData)F(NewMediaData)F(FreeMediaData)F(GetData)F(GetDataLen)F(GetOutIndexBuf)F(GetIndexLen)F(IsMediaDataFinish)
#undef F
return 0;} static void closeapi(api*a){if(a->h)dlclose(a->h);}
static int archive_allocations=0; static void* archive_malloc(size_t n){void*p=malloc(n);if(p)archive_allocations++;return p;} static void archive_memfree(void*p){if(p){archive_allocations--;free(p);}}
static void archive_write_stats(void){const char*path=getenv("ARCHIVE_RUNNER_ALLOC_STATS");if(path){FILE*f=fopen(path,"a");if(f){fprintf(f,"runner_allocations=%d\n",archive_allocations);fclose(f);}}}
static int copy(char*src,int len,char**out,int*outlen){if(len<0||len>ARCHIVE_MAX_RESPONSE_BYTES||(len>0&&!src))return -3;size_t size=(size_t)(len?len:1);*out=archive_malloc(size);if(!*out)return -4;if(len)memcpy(*out,src,(size_t)len);*outlen=len;return 0;}
int archive_health(const char*p){api a;int r=load(p,&a);if(r)return r;Sdk*s=a.NewSdk();if(!s){closeapi(&a);return -5;}a.DestroySdk(s);closeapi(&a);return 0;}
int archive_chat(const char*p,const char*c,const char*sec,unsigned long long seq,unsigned int limit,char**out,int*n){api a;int r=load(p,&a);if(r)return r;Sdk*s=a.NewSdk();Slice*x=a.NewSlice();if(!s||!x){if(x)a.FreeSlice(x);if(s)a.DestroySdk(s);closeapi(&a);return -5;}r=a.Init(s,c,sec);if(!r)r=a.GetChatData(s,seq,limit,"","",10,x);if(!r)r=copy(a.GetContentFromSlice(x),a.GetSliceLen(x),out,n);a.FreeSlice(x);a.DestroySdk(s);closeapi(&a);return r;}
int archive_decrypt(const char*p,const char*k,const char*m,char**out,int*n){api a;int r=load(p,&a);if(r)return r;Slice*x=a.NewSlice();if(!x){closeapi(&a);return -5;}r=a.DecryptData(k,m,x);if(!r)r=copy(a.GetContentFromSlice(x),a.GetSliceLen(x),out,n);a.FreeSlice(x);closeapi(&a);return r;}
int archive_media(const char*p,const char*c,const char*sec,const char*file,const char*idx,char**out,int*n,char**next,int*nn,int*finish){*out=NULL;*n=0;*next=NULL;*nn=0;*finish=0;api a;int r=load(p,&a);if(r)return r;Sdk*s=a.NewSdk();Media*x=a.NewMediaData();if(!s||!x){if(x)a.FreeMediaData(x);if(s)a.DestroySdk(s);closeapi(&a);return -5;}r=a.Init(s,c,sec);if(!r)r=a.GetMediaData(s,idx,file,"","",10,x);if(!r)r=copy(a.GetData(x),a.GetDataLen(x),out,n);if(!r)r=copy(a.GetOutIndexBuf(x),a.GetIndexLen(x),next,nn);if(r){archive_memfree(*out);archive_memfree(*next);*out=NULL;*n=0;*next=NULL;*nn=0;}if(!r)*finish=a.IsMediaDataFinish(x);a.FreeMediaData(x);a.DestroySdk(s);closeapi(&a);return r;}int archive_decrypt_many(const char*p,const char**keys,const char**msgs,int count,char***outs,int**lens){api a;int r=load(p,&a);if(r)return r;size_t total=0;char**data=archive_malloc((size_t)count*sizeof(char*));int*lengths=archive_malloc((size_t)count*sizeof(int));if(data)memset(data,0,(size_t)count*sizeof(char*));if(lengths)memset(lengths,0,(size_t)count*sizeof(int));if(!data||!lengths){archive_memfree(data);archive_memfree(lengths);closeapi(&a);return -4;}for(int i=0;i<count;i++){Slice*x=a.NewSlice();if(!x){r=-5;break;}r=a.DecryptData(keys[i],msgs[i],x);if(!r)r=copy(a.GetContentFromSlice(x),a.GetSliceLen(x),&data[i],&lengths[i]);if(!r&&((size_t)lengths[i]>ARCHIVE_MAX_RESPONSE_BYTES-total))r=-3;else if(!r)total+=(size_t)lengths[i];a.FreeSlice(x);if(r)break;}if(r){for(int i=0;i<count;i++)archive_memfree(data[i]);archive_memfree(data);archive_memfree(lengths);closeapi(&a);return r;}*outs=data;*lens=lengths;closeapi(&a);return 0;} void archive_free(void*p){archive_memfree(p);}
*/
import "C"
import (
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/archivesdk"
	"strconv"
	"unsafe"
)

func nativeRun(r archivesdk.Request) archivesdk.Response {
	if r.LibraryPath == "" {
		return archivesdk.Response{ErrorCode: "sdk_unavailable"}
	}
	p := C.CString(r.LibraryPath)
	defer C.free(unsafe.Pointer(p))
	if r.Operation == "health" {
		code := C.archive_health(p)
		if code != 0 {
			return archivesdk.Response{ErrorCode: "sdk_load_failed"}
		}
		return archivesdk.Response{LibraryLoadable: true, HandleCreated: true}
	}
	c := C.CString(r.CorpID)
	s := C.CString(r.Secret)
	defer C.free(unsafe.Pointer(c))
	defer C.free(unsafe.Pointer(s))
	var out *C.char
	var n C.int
	var code C.int
	switch r.Operation {
	case "decrypt_batch":
		if len(r.DecryptItems) == 0 || len(r.DecryptItems) > 1000 {
			return archivesdk.Response{ErrorCode: "invalid_operation"}
		}
		keys := make([]*C.char, len(r.DecryptItems))
		msgs := make([]*C.char, len(r.DecryptItems))
		for i, item := range r.DecryptItems {
			keys[i] = C.CString(item.DecryptKey)
			msgs[i] = C.CString(item.EncryptedMessage)
			defer C.free(unsafe.Pointer(keys[i]))
			defer C.free(unsafe.Pointer(msgs[i]))
		}
		var outs **C.char
		var lens *C.int
		code = C.archive_decrypt_many(p, (**C.char)(unsafe.Pointer(&keys[0])), (**C.char)(unsafe.Pointer(&msgs[0])), C.int(len(keys)), &outs, &lens)
		if code != 0 {
			return archivesdk.Response{ErrorCode: "sdk_" + strconv.Itoa(int(code))}
		}
		defer C.archive_free(unsafe.Pointer(outs))
		defer C.archive_free(unsafe.Pointer(lens))
		rawOut := (*[1 << 28]*C.char)(unsafe.Pointer(outs))[:len(keys):len(keys)]
		rawLens := (*[1 << 28]C.int)(unsafe.Pointer(lens))[:len(keys):len(keys)]
		items := make([][]byte, len(keys))
		for i := range rawOut {
			if !safeCLength(rawLens[i]) {
				for j := i; j < len(rawOut); j++ {
					C.archive_free(unsafe.Pointer(rawOut[j]))
				}
				return archivesdk.Response{ErrorCode: "sdk_output_invalid"}
			}
			items[i] = C.GoBytes(unsafe.Pointer(rawOut[i]), rawLens[i])
			C.archive_free(unsafe.Pointer(rawOut[i]))
			rawOut[i] = nil
		}
		return archivesdk.Response{Items: items}
	case "fetch":
		code = C.archive_chat(p, c, s, C.ulonglong(r.Seq), C.uint(r.Limit), &out, &n)
	case "decrypt":
		k := C.CString(r.DecryptKey)
		m := C.CString(r.EncryptedMessage)
		defer C.free(unsafe.Pointer(k))
		defer C.free(unsafe.Pointer(m))
		code = C.archive_decrypt(p, k, m, &out, &n)
	case "media":
		file := C.CString(r.FileID)
		idx := C.CString(r.IndexBuf)
		defer C.free(unsafe.Pointer(file))
		defer C.free(unsafe.Pointer(idx))
		var next *C.char
		var nn C.int
		var finished C.int
		code = C.archive_media(p, c, s, file, idx, &out, &n, &next, &nn, &finished)
		if code == 0 {
			if !safeCLength(n) || !safeCLength(nn) {
				C.archive_free(unsafe.Pointer(out))
				C.archive_free(unsafe.Pointer(next))
				return archivesdk.Response{ErrorCode: "sdk_output_invalid"}
			}
			data := C.GoBytes(unsafe.Pointer(out), n)
			index := C.GoStringN(next, nn)
			C.archive_free(unsafe.Pointer(out))
			C.archive_free(unsafe.Pointer(next))
			return archivesdk.Response{Data: data, NextIndexBuf: index, Finished: finished != 0}
		}
	default:
		return archivesdk.Response{ErrorCode: "invalid_operation"}
	}
	if code != 0 {
		return archivesdk.Response{ErrorCode: "sdk_" + strconv.Itoa(int(code))}
	}
	if !safeCLength(n) {
		C.archive_free(unsafe.Pointer(out))
		return archivesdk.Response{ErrorCode: "sdk_output_invalid"}
	}
	data := C.GoBytes(unsafe.Pointer(out), n)
	C.archive_free(unsafe.Pointer(out))
	return archivesdk.Response{Data: data}
}

func nativeShutdown() { C.archive_write_stats() }

func safeCLength(length C.int) bool {
	// []byte becomes base64 inside the JSON response frame, so keep native
	// buffers below half the protocol frame limit before GoBytes allocates.
	return length >= 0 && length <= C.int(archivesdk.MaxFrame/2)
}
