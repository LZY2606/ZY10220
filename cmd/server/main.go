// Command server 启动固结路径查验台本地服务。
package main

import (
	"flag"
	"log"
	"net/http"

	"consolidation-console/internal/httpapi"
	"consolidation-console/internal/store"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:5560", "监听地址")
	dbPath := flag.String("db", "consolidation.db", "SQLite 数据库文件")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()

	srv, err := httpapi.New(st)
	if err != nil {
		log.Fatalf("初始化服务失败: %v", err)
	}
	log.Printf("固结路径查验台: http://%s", *listen)
	if err := http.ListenAndServe(*listen, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
