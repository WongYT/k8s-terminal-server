package lib

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gorilla/websocket"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

var (
	mConfig    *rest.Config
	mClientset *kubernetes.Clientset
)

var terminalSessions = make(map[string]TerminalSession)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	}}

// PtyHandler is what remotecommand expects from a pty
type PtyHandler interface {
	io.Reader
	io.Writer
	io.Closer
	remotecommand.TerminalSizeQueue
}

// TerminalSession implements PtyHandler (using a SockJS connection)
type TerminalSession struct {
	id       string
	sockConn *websocket.Conn
	sizeChan chan remotecommand.TerminalSize
	bound    chan error

	receiver chan []byte
	sender   chan []byte
}

// TerminalSize handles pty->process resize events
// Called in a loop from remotecommand as long as the process is running
func (t TerminalSession) Next() *remotecommand.TerminalSize {
	select {
	case size := <-t.sizeChan:
		return &size
	}
}

// Read handles pty->process messages (stdin, resize)
// Called in a loop from remotecommand as long as the process is running
func (t TerminalSession) Read(p []byte) (int, error) {
	m := <-t.receiver
	return copy(p, m), nil
}

// Write handles process->pty stdout
// Called from remotecommand whenever there is any output
func (t TerminalSession) Write(p []byte) (int, error) {
	err := t.sockConn.WriteMessage(websocket.TextMessage, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// Toast can be used to send the user any OOB messages
// hterm puts these in the center of the terminal
func (t TerminalSession) Toast(p string) error {
	if err := t.sockConn.WriteMessage(websocket.TextMessage, []byte(p)); err != nil {
		return err
	}
	return nil
}

// Close shuts down the SockJS connection and sends the status code and reason to the client
// Can happen if the process exits or if there is an error starting up the process
// For now the status code is unused and reason is shown to the user (unless "")
func (t TerminalSession) Close() error {
	//log.Println("Terminal session was closed")
	if err := t.sockConn.Close(); err != nil {
		return err
	}
	return nil
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}

func loadConfig() *rest.Config {
	if mConfig == nil {
		var kubeconfig *string
		if home := homeDir(); home != "" {
			kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"),
				"(optional) absolute path to the kubeconfig file")
		} else {
			kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
		}
		flag.Parse()

		// use the current context in kubeconfig
		config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			panic(err.Error())
		}
		mConfig = config
	}
	return mConfig
}

func getClientSet() *kubernetes.Clientset {
	if mClientset == nil {
		config := loadConfig()
		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			panic(err.Error())
		}
		mClientset = clientset
	}
	return mClientset
}

func execPod(container string, pod string, namespace string, cmd []string,
	ptyHandler PtyHandler) error {

	config := loadConfig()
	clientset := getClientSet()

	req := clientset.CoreV1().RESTClient().Post().Resource("pods").Name(pod).
		Namespace(namespace).SubResource("exec")

	req.VersionedParams(&v1.PodExecOptions{
		Container: container,
		Command:   cmd,
		Stdin:     true,
		Stdout:    true,
		Stderr:    true,
		TTY:       true,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return err
	}

	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:             ptyHandler,
		Stdout:            ptyHandler,
		Stderr:            ptyHandler,
		TerminalSizeQueue: ptyHandler,
		Tty:               true,
	})
	if err != nil {
		return err
	}
	return nil
}

func GenTerminalSessionId() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	id := make([]byte, hex.EncodedLen(len(bytes)))
	hex.Encode(id, bytes)
	return string(id), nil
}

func CreateSession(w http.ResponseWriter, r *http.Request) (string, error) {

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
	}
	sessionId, _ := GenTerminalSessionId()
	terminalSession := TerminalSession{
		id:       sessionId,
		sockConn: conn,
		bound:    make(chan error),
		sizeChan: make(chan remotecommand.TerminalSize),

		receiver: make(chan []byte),
		sender:   make(chan []byte),
	}
	terminalSessions[sessionId] = terminalSession
	return sessionId, nil
}

func readFromWebTerminal(sessionId string) {
	for {
		_, message, err := terminalSessions[sessionId].sockConn.ReadMessage()
		if err != nil {
			log.Printf("error: %v", err)
			break
		}
		terminalSessions[sessionId].receiver <- message
	}
	log.Println("readFromWebTerminal ReadMessage was closed")
}

func GetPodListByLable(namespace string, labels string) ([]string, error) {
	clientset := getClientSet()
	option := metav1.ListOptions{
		LabelSelector: labels,
	}
	pods, err := clientset.CoreV1().Pods(namespace).List(option)
	if err != nil {
		return nil, err
	}

	len := len(pods.Items)
	fmt.Printf("There are %d pods in the cluster\n", len)

	podNames := make([]string, len)
	for i := 0; i < len; i++ {
		podNames[i] = pods.Items[i].Name
	}
	return podNames, nil
}

func ExecTerminal(container string, pod string, namespace string, sessionId string) {

	defer terminalSessions[sessionId].Close()
	go readFromWebTerminal(sessionId)

	shells := []string{"bash", "sh"}
	var err error
	for _, shell := range shells {
		cmd := []string{shell}
		if err = execPod(container, pod, namespace, cmd, terminalSessions[sessionId]); err == nil {
			break
		}
		log.Println("ExecTerminal execPod err", err)
	}

	if err != nil {
		log.Println("ExecTerminal err", err)
		return
	}
	log.Println("terminal was closed")
}
