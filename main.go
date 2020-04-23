package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"text/template"
)

func main() {
	addr := flag.String("address", ":80", "Address to host the HTTP server on.")
	flag.Parse()

	log.Println("Listening on", *addr)
	err := serve(*addr)
	if err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}

func serve(addr string) (err error) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			temp := template.Must(template.ParseFiles("template.html"))
			data := struct{ Title string }{Title: "Reflection test"}
			err = temp.Execute(w, data)
			if err != nil {
				fmt.Println(err)
				return
			}
		} else {
			http.FileServer(http.Dir(".")).ServeHTTP(w, r)
		}
	})

	return http.ListenAndServe(addr, nil)
}
