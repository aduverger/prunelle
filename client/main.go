package main

import (
	"embed"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/fetch"
)

//go:embed all:dist
var frontend embed.FS

func apiTarget(r *http.Request) bool {
	u := r.URL
	if u.User != nil || !strings.HasPrefix(u.Path, "/v1/") {
		return false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	local := host == "localhost" || (ip != nil && ip.IsLoopback())
	return (local && (u.Scheme == "http" || u.Scheme == "https")) || (u.Scheme == "https" && strings.HasSuffix(host, ".ts.net"))
}

func main() {
	web, err := fs.Sub(frontend, "dist")
	if err != nil {
		log.Fatal(err)
	}
	mygo.SetFrontend(web)
	mygo.Use(fetch.New(fetch.Options{Allow: apiTarget, Client: &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}))
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{Title: "Prunelle", URL: "/", Width: 1500, Height: 960, MinWidth: 960, MinHeight: 640})
		win.Page().OnWillNavigate(func(e *mygo.NavigateEvent) { e.PreventDefault() })
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
