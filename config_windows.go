package main

import (
	"os"
	"path"
)

func GetConfigDir() string {
	path.Join(os.Getenv("APPDATA"))
}
