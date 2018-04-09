package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/urfave/negroni"

	"./lib"
)

func AuthMiddleware(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	fmt.Println("auth middleware")
	next(rw, r)
}

func HomeHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Hello! This is terminal server.")
}

func GetPodHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	label := vars["label"]
	namespace := vars["namespace"]
	pods, _ := lib.GetPodListByLable(namespace, label)

	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, pods)
}

func checkJwtToken(token string) bool {
	return lib.IsVaildJwtToken(token)
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	}}

func TerminalHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pod := vars["pod"]
	container := vars["container"]
	namespace := vars["namespace"]
	jwtToken := vars["jwtToken"]
	log.Printf("TerminalHandler namespace=%s, pod=%s, container=%s", namespace, pod, container)

	if checkJwtToken(jwtToken) {
		sessionId, err := lib.CreateSession(w, r)
		log.Printf("start terminal: %s\n", sessionId)
		if err == nil {
			go lib.ExecTerminal(container, pod, namespace, sessionId)
		}
	} else {
		log.Println("token is invaild or expired")
	}
}

func main() {
	router := mux.NewRouter()
	router.HandleFunc("/", HomeHandler).Methods("GET")
	router.HandleFunc("/api/v1/pods/{namespace}/{label}", GetPodHandler).Methods("GET")
	router.HandleFunc("/api/v1/terminals/{namespace}/{pod}/{container}", TerminalHandler).
		Queries("jwtToken", "{jwtToken}")

	//n := negroni.Classic()
	n := negroni.New()
	n.Use(negroni.HandlerFunc(AuthMiddleware))
	n.UseHandler(router)

	log.Println("Start server on 8000")
	log.Fatal(http.ListenAndServe(":8000", n))
}
