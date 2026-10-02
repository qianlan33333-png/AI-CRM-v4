package main

import (
	"github.com/qianlan33333-png/AI-CRM-v3/internal/crmcli"
	"os"
)

func main() { os.Exit(crmcli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
