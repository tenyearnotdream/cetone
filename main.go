// Command cetone is a NetEase Cloud Music client built with Viva: no web view,
// a Go API client, and an audio engine that plays a cached file before it asks
// the server for a URL.
//
// The one feature this client keeps from its GTK ancestor: a song cached while
// a VIP subscription was active still plays after the subscription lapses,
// because the cache is checked first and its file names are the GTK client's.
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/vivaui/viva/app"

	vivaapp "cetone/internal/app"
	"cetone/internal/store"
)

// version is reported by --version and in the window's title.
const version = "1.0.0"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			fmt.Printf("cetone %s\n", version)
			return
		case "--help", "-h":
			fmt.Println("Cetone — 网易云音乐客户端（Viva）")
			fmt.Println("\n用法: cetone [--version] [--help]")
			fmt.Println("\n配置、缓存与会话保存在 XDG 目录下；旧 GTK 版的音频缓存会被自动识别并优先读取。")
			return
		}
	}

	paths := store.DefaultPaths()
	config := app.Config{
		Title:     "Cetone — 网易云音乐",
		Class:     "cetone",
		Width:     1160,
		Height:    740,
		MinWidth:  880,
		MinHeight: 560,
	}
	err := vivaapp.Run(config, paths)
	if err == nil {
		return
	}
	// A GPU driver that cannot provide the surfaces Viva needs leaves the
	// window unopened. Viva can draw the same page on the CPU instead, through
	// VIVA_SOFTWARE_RENDERING, so the program restarts itself with that set
	// rather than asking the user to know it. The retry happens at most once,
	// and it is logged: a silent fallback would hide a broken driver.
	if vivaapp.IsRendererFailure(err) && os.Getenv("VIVA_SOFTWARE_RENDERING") != "1" {
		log.Printf("窗口无法在 GPU 上创建（%v），改用 CPU 渲染重试", err)
		if retryErr := restartWithSoftwareRendering(); retryErr != nil {
			log.Fatalf("%v（CPU 渲染重试失败：%v）", err, retryErr)
		}
		return
	}
	log.Fatal(err)
}
