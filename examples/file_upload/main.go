package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
	
	"github.com/oarkflow/ss"
	"github.com/oarkflow/ss/chi"
)

const HandshakeTimeoutSecs = 10

type UploadHeader struct {
	Filename string
	Size     int
}

type UploadStatus struct {
	Code   int    `json:"code,omitempty"`
	Status string `json:"status,omitempty"`
	Pct    *int   `json:"pct,omitempty"` // File processing AFTER upload is done.
	pct    int
}

type wsConn struct {
	conn *ss.Conn
}

func upload(w http.ResponseWriter, r *http.Request) {
	wsc := wsConn{}
	var err error
	
	// Open websocket connection.
	upgrader := ss.Upgrader{HandshakeTimeout: time.Second * HandshakeTimeoutSecs}
	wsc.conn, err = upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Error on open of websocket connection:", err)
		return
	}
	defer wsc.conn.Close()
	
	// Get upload file name and length.
	header := new(UploadHeader)
	mt, message, err := wsc.conn.ReadMessage()
	if err != nil {
		fmt.Println("Error receiving websocket message:", err)
		return
	}
	if mt != ss.TextMessage {
		wsc.sendStatus(400, "Invalid message received, expecting file name and length")
		return
	}
	if err := json.Unmarshal(message, header); err != nil {
		wsc.sendStatus(400, "Error receiving file name and length: "+err.Error())
		return
	}
	if len(header.Filename) == 0 {
		wsc.sendStatus(400, "Filename cannot be empty")
		return
	}
	if header.Size == 0 {
		wsc.sendStatus(400, "Upload file is empty")
		return
	}
	
	// Create temp file to save file.
	var tempFile *os.File
	if tempFile, err = os.CreateTemp("", "websocket_upload_"); err != nil {
		wsc.sendStatus(400, "Could not create temp file: "+err.Error())
		return
	}
	defer func() {
		tempFile.Close()
		// *** IN PRODUCTION FILE SHOULD BE REMOVED AFTER PROCESSING ***
		// _ = os.Remove(tempFile.Name())
	}()
	
	// Read file blocks until all bytes are received.
	bytesRead := 0
	for {
		mt, message, err := wsc.conn.ReadMessage()
		if err != nil {
			wsc.sendStatus(400, "Error receiving file block: "+err.Error())
			return
		}
		if mt != ss.BinaryMessage {
			if mt == ss.TextMessage {
				if string(message) == "CANCEL" {
					wsc.sendStatus(400, "Upload canceled")
					return
				}
			}
			wsc.sendStatus(400, "Invalid file block received")
			return
		}
		
		tempFile.Write(message)
		
		bytesRead += len(message)
		if bytesRead == header.Size {
			tempFile.Close()
			break
		}
		
		wsc.requestNextBlock()
	}
	
	// *****************************
	// *** Process uploaded file ***
	// *****************************
	for i := 0; i <= 10; i++ {
		wsc.sendPct(i * 10)
		time.Sleep(time.Second * 1)
	}
	wsc.sendStatus(200, "File upload successful: "+fmt.Sprintf("%s (%d bytes)", tempFile.Name(), bytesRead))
}

func (wsc wsConn) requestNextBlock() {
	wsc.conn.WriteMessage(ss.TextMessage, []byte("NEXT"))
}

func (wsc wsConn) sendStatus(code int, status string) {
	if msg, err := json.Marshal(UploadStatus{Code: code, Status: status}); err == nil {
		wsc.conn.WriteMessage(ss.TextMessage, msg)
	}
}

func (wsc wsConn) sendPct(pct int) {
	stat := UploadStatus{pct: pct}
	stat.Pct = &stat.pct
	if msg, err := json.Marshal(stat); err == nil {
		wsc.conn.WriteMessage(ss.TextMessage, msg)
	}
}

func showJS(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%s", webPage)
}

func main() {
	server := chi.NewRouter()
	server.HandleFunc("/upload", upload)
	server.Mount("/", http.FileServer(http.Dir("./webroot")))
	log.Fatal(http.ListenAndServe(":8084", server))
}

var webPage = `

`
