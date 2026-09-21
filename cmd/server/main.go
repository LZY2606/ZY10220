// Command server runs the consolidation path inspection console.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"consolidation/internal/store"
	"consolidation/internal/web"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:5560", "listen address")
	dbPath := flag.String("db", "consolidation.db", "SQLite database path")
	importFile := flag.String("import", "", "import an exported run-record JSON before serving")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer st.Close()

	if *importFile != "" {
		data, err := os.ReadFile(*importFile)
		if err != nil {
			log.Fatalf("read import file: %v", err)
		}
		if err := st.ImportJSON(data); err != nil {
			log.Fatalf("import: %v", err)
		}
		log.Printf("imported run record from %s", *importFile)
	}
	seeded, err := st.SeedIfEmpty()
	if err != nil {
		log.Fatalf("seed: %v", err)
	}
	if seeded {
		log.Printf("database empty, seeded fixture run FIX-01")
	}

	srv := web.New(st)
	fmt.Printf("固结路径查验台 listening on http://%s\n", *listen)
	log.Fatal(http.ListenAndServe(*listen, srv))
}
