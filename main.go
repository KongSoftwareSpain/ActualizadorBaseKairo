package main

import (
	"embed"
	"log"
	"os"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"actualizador/internal/importer"
	"actualizador/internal/migrator"
	"actualizador/internal/silent"
	"actualizador/internal/userconfig"
)

// silentPassword is the password of the installer login used by --silent. It
// is set by silent_password_local.go, generated at build time by
// build/windows/silent-password.ps1 and ignored by git, so it is not in the
// source. Empty: the silent mode always falls back to the window.
var silentPassword string

func init() {
	application.RegisterEvent[migrator.Event](migrator.EventName)
	application.RegisterEvent[importer.Event](importer.EventName)
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

	// There is no window while the silent run goes on: events only reach the log file.
	var app *application.App

	migratorService := migrator.NewService(baseDir, func(event migrator.Event) {
		if app != nil {
			app.Event.Emit(migrator.EventName, event)
		}
	})

	// The import uses the same kong login as --silent, so it shares its password.
	importService := importer.NewService(baseDir, silentPassword, func(event importer.Event) {
		if app != nil {
			app.Event.Emit(importer.EventName, event)
		}
	})

	runSilent(baseDir, migratorService)

	app = application.New(application.Options{
		Name:        "Actualizador",
		Description: "Actualizador de bases de datos SQL Server",
		Services: []application.Service{
			application.NewService(&userconfig.Service{}),
			application.NewService(migratorService),
			application.NewService(importService),
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

	confirmCloseWhileUpdating(app, window, func() bool { return migratorService.IsRunning() || importService.IsRunning() })

	err = app.Run()
	if err != nil {
		log.Fatal(err)
	}
}

// confirmCloseWhileUpdating asks before closing the window during an update.
// Closing aborts the running script (SQL Server rolls it back), leaving the
// database half updated and the log cut short.
func confirmCloseWhileUpdating(app *application.App, window *application.WebviewWindow, isBusy func() bool) {
	var isCloseConfirmed atomic.Bool

	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if isCloseConfirmed.Load() || !isBusy() {
			return
		}

		event.Cancel()

		dialog := app.Dialog.Question().
			SetTitle("Actualización en curso").
			SetMessage("Hay una actualización o una importación en curso. Si cierras ahora, se interrumpe el script que se está ejecutando " +
				"(SQL Server lo revierte) y la base de datos queda a medias. ¿Cerrar de todos modos?").
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

// runSilent handles --silent. When the update finishes it ends the process
// with code 0 and no window. On any problem it logs why, fills in the server
// and database of the window that opens next, and returns so that a technician
// can take over.
func runSilent(baseDir string, service *migrator.Service) {
	options := silent.ParseArgs(os.Args[1:])
	if !options.IsEnabled {
		return
	}

	options.Password = silentPassword

	connection, err := silent.Run(service, options)
	if err == nil {
		os.Exit(0)
	}

	if logErr := silent.LogFailure(baseDir, err); logErr != nil {
		log.Printf("silent: %v (y no se pudo escribir el log: %v)", err, logErr)
	}

	if prefillErr := silent.Prefill(&userconfig.Service{}, connection); prefillErr != nil {
		log.Printf("silent: no se pudo rellenar la conexión: %v", prefillErr)
	}
}
