package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"changeme/internal/userconfig"
)

// Wails embeds the built frontend into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := application.New(application.Options{
		Name:        "My Product",
		Description: "A MigradorKairo application",
		Services: []application.Service{
			application.NewService(&userconfig.Service{}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	registerContextMenus()

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "My Product",
		Width:            1000,
		Height:           800,
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	err := app.Run()
	if err != nil {
		log.Fatal(err)
	}
}

// registerContextMenus defines the only right-click menus of the app, in
// place of the WebView2 ones (Emoji, passwords, spelling...). The frontend
// picks one with the `--custom-contextmenu` CSS property (see style.css).
func registerContextMenus() {
	editMenu := application.NewContextMenu("edit")
	editMenu.AddRole(application.Cut)
	editMenu.AddRole(application.Copy)
	editMenu.AddRole(application.Paste)
	editMenu.AddSeparator()
	editMenu.AddRole(application.SelectAll)
	setMenuLabel(editMenu, application.Cut, "Cortar")
	setMenuLabel(editMenu, application.Copy, "Copiar")
	setMenuLabel(editMenu, application.Paste, "Pegar")
	setMenuLabel(editMenu, application.SelectAll, "Seleccionar todo")
	editMenu.Update()

	copyMenu := application.NewContextMenu("copy")
	copyMenu.AddRole(application.Copy)
	copyMenu.AddRole(application.SelectAll)
	setMenuLabel(copyMenu, application.Copy, "Copiar")
	setMenuLabel(copyMenu, application.SelectAll, "Seleccionar todo")
	copyMenu.Update()

	pasteMenu := application.NewContextMenu("paste")
	pasteMenu.AddRole(application.Paste)
	pasteMenu.AddRole(application.SelectAll)
	setMenuLabel(pasteMenu, application.Paste, "Pegar")
	setMenuLabel(pasteMenu, application.SelectAll, "Seleccionar todo")
	pasteMenu.Update()
}

func setMenuLabel(menu *application.ContextMenu, role application.Role, label string) {
	item := menu.FindByRole(role)
	if item != nil {
		item.SetLabel(label)
	}
}
