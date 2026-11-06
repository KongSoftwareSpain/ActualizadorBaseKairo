package main

import (
	"embed"
	"log"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"actualizador/internal/migrator"
	"actualizador/internal/userconfig"
)

func init() {
	application.RegisterEvent[migrator.Event](migrator.EventName)
}

// Wails embeds the built frontend into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	baseDir, err := migrator.DefaultBaseDir()
	if err != nil {
		log.Fatal(err)
	}

	migratorService := migrator.NewService(baseDir, func(event migrator.Event) {
		application.Get().Event.Emit(migrator.EventName, event)
	})

	app := application.New(application.Options{
		Name:        "Actualizador",
		Description: "Actualizador de bases de datos SQL Server",
		Services: []application.Service{
			application.NewService(&userconfig.Service{}),
			application.NewService(migratorService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	registerContextMenus()

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Actualizador",
		Width:            1000,
		Height:           800,
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	confirmCloseWhileUpdating(app, window, migratorService)

	err = app.Run()
	if err != nil {
		log.Fatal(err)
	}
}

// confirmCloseWhileUpdating asks before closing the window during an update.
// Closing aborts the running script (SQL Server rolls it back), leaving the
// database half updated and the log cut short.
func confirmCloseWhileUpdating(app *application.App, window *application.WebviewWindow, service *migrator.Service) {
	var isCloseConfirmed atomic.Bool

	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if isCloseConfirmed.Load() || !service.IsRunning() {
			return
		}

		event.Cancel()

		dialog := app.Dialog.Question().
			SetTitle("Actualización en curso").
			SetMessage("Hay una actualización en curso. Si cierras ahora, se interrumpe el script que se está ejecutando " +
				"(SQL Server lo revierte) y la base de datos queda a medio actualizar. ¿Cerrar de todos modos?").
			AttachToWindow(window)

		closeButton := dialog.AddButton("Cerrar de todos modos")
		closeButton.OnClick(func() {
			isCloseConfirmed.Store(true)
			window.Close()
		})

		keepButton := dialog.AddButton("Seguir esperando")
		keepButton.SetAsDefault().SetAsCancel()

		dialog.SetDefaultButton(keepButton)
		dialog.SetCancelButton(keepButton)
		dialog.Show()
	})
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
