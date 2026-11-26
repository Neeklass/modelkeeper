package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"log"
	"strings"

	"modelkeeper/internal/appdir"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "modelkeeper",
		Width:     1280,
		Height:    800,
		MinWidth:  800,
		MinHeight: 500,
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: assetHandler{app}, // serves /thumbs/ and /files/
		},
		BackgroundColour: &options.RGBA{R: 24, G: 24, B: 27, A: 1},
		// One instance per data folder: a second start (a double click while
		// it is open) brings the open window to the front instead of running
		// two programs on the same catalog. Two installations with different
		// data folders can still run side by side.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: instanceID(),
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				if app.ctx == nil {
					return
				}
				runtime.WindowUnminimise(app.ctx)
				runtime.Show(app.ctx)
			},
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// instanceID identifies the data folder for the single-instance lock.
func instanceID() string {
	key := "default"
	if l, err := appdir.Resolve(); err == nil {
		key = strings.ToLower(l.Root)
	}
	sum := sha256.Sum256([]byte(key))
	return "modelkeeper-" + hex.EncodeToString(sum[:8])
}
