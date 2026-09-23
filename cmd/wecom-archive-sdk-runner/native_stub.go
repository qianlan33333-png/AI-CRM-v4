//go:build !linux || !cgo

package main

import "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/archivesdk"

func nativeRun(archivesdk.Request) archivesdk.Response {
	return archivesdk.Response{ErrorCode: "sdk_unavailable"}
}

func nativeShutdown() {}
