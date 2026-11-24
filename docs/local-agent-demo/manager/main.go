package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bytedance/Elkeid/agent/proto"
	_ "github.com/bytedance/Elkeid/agent/transport/compressor"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	pluginName     = "demo-fixture"
	fixtureRecord  = 9990
	fixtureContent = "Elkeid local demo upload\n"
	uploadToken    = "local-demo-upload"
	fixtureDetail  = "loopback-fixture"
)

type demo struct {
	proto.UnimplementedTransferServer
	proto.UnimplementedFileExtServer
	pluginConfig *proto.Config
	plugin       []byte
	scanner      *scannerDemo
}

func newDemo(pluginURL string) *demo {
	record, err := (&proto.Record{
		DataType:  fixtureRecord,
		Timestamp: time.Now().Unix(),
		Data:      &proto.Payload{Fields: map[string]string{"message": "local fixture started", "detail": fixtureDetail}},
	}).Marshal()
	if err != nil {
		panic(err)
	}
	frame := make([]byte, 4)
	binary.LittleEndian.PutUint32(frame, uint32(len(record)))
	frame = append(frame, record...)
	var script strings.Builder
	script.WriteString("#!/bin/sh\n[ \"$DETAIL\" = '" + fixtureDetail + "' ] || exit 1\nprintf '")
	for _, b := range frame {
		fmt.Fprintf(&script, "\\%03o", b)
	}
	script.WriteString("' >&4\nexec /bin/cat <&3 >/dev/null\n")
	plugin := []byte(script.String())
	checksum := fmt.Sprintf("%x", sha256.Sum256(plugin))
	return &demo{
		plugin: plugin,
		pluginConfig: &proto.Config{
			Name: pluginName, Type: "raw", Version: "local-demo-v1",
			Sha256: checksum, Signature: checksum, DownloadUrls: []string{pluginURL}, Detail: fixtureDetail,
		},
	}
}

func (d *demo) Transfer(stream proto.Transfer_TransferServer) error {
	sent := false
	scannerTask := ""
	for {
		batch, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !sent && batch.Product == "elkeid-agent" && batch.AgentId != "" {
			log.Printf("event=agent_connected agent_id=%q version=%q", batch.AgentId, batch.Version)
			configs := []*proto.Config{d.pluginConfig}
			if d.scanner != nil {
				configs = append(configs, d.scanner.config)
			}
			if err := stream.Send(&proto.Command{Configs: configs}); err != nil {
				return err
			}
			log.Printf("event=config_sent plugin=%s sha256=%s", d.pluginConfig.Name, d.pluginConfig.Sha256)
			if err := stream.Send(&proto.Command{Task: &proto.Task{
				DataType: 1050, ObjectName: "elkeid-agent", Token: uploadToken,
				Data: `{"path":"demo-upload.txt","buf_size":4096}`,
			}}); err != nil {
				return err
			}
			log.Printf("event=upload_task_sent token=%s", uploadToken)
			sent = true
		}
		for _, record := range batch.Records {
			payload := new(proto.Payload)
			if err := payload.Unmarshal(record.Data); err != nil {
				log.Printf("event=record_decode_failed data_type=%d error=%q", record.DataType, err)
				continue
			}
			switch record.DataType {
			case 1000:
				log.Printf("event=agent_heartbeat data_type=1000 state=%q timestamp=%d", payload.Fields["state"], record.Timestamp)
			case 1001:
				log.Printf("event=plugin_heartbeat data_type=1001 name=%q timestamp=%d", payload.Fields["name"], record.Timestamp)
				if sent && d.scanner != nil && scannerTask == "" && payload.Fields["name"] == "scanner" {
					scannerTask = "demo-hit"
					if err := d.scanner.sendTask(stream, scannerTask); err != nil {
						return err
					}
				}
			case fixtureRecord:
				log.Printf("event=fixture_record data_type=%d message=%q detail=%q timestamp=%d payload_hex=%x", fixtureRecord, payload.Fields["message"], payload.Fields["detail"], record.Timestamp, record.Data)
			case 6003:
				fields := payload.Fields
				if d.scanner != nil && scannerTask != "" && fields["token"] == scannerTask {
					log.Printf("event=scanner_report data_type=6003 token=%q types=%q class=%q name=%q error=%q timestamp=%d", fields["token"], fields["types"], fields["class"], fields["name"], fields["error"], record.Timestamp)
					if scannerTask == "demo-hit" {
						scannerTask = "demo-clean"
						if err := d.scanner.sendTask(stream, scannerTask); err != nil {
							return err
						}
					} else {
						scannerTask = "complete"
					}
				}
			default:
				log.Printf("event=telemetry data_type=%d", record.DataType)
			}
		}
	}
}

func (d *demo) Upload(stream proto.FileExt_UploadServer) error {
	response := &proto.FileUploadResponse{Status: proto.FileUploadResponse_FAILED}
	var received []byte
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if bytes.Equal(received, []byte(fixtureContent)) {
				response.Status = proto.FileUploadResponse_SUCCESS
				log.Printf("event=upload_verified token=%s bytes=%d sha256=%x", uploadToken, len(received), sha256.Sum256(received))
			}
			return stream.SendAndClose(response)
		}
		if err != nil {
			return err
		}
		if request.Token != uploadToken || len(request.Data) > len(fixtureContent)-len(received) {
			return stream.SendAndClose(response)
		}
		received = append(received, request.Data...)
	}
}

func serverTLS(repo string) (*tls.Config, error) {
	server := filepath.Join(repo, "server", "agent_center", "conf")
	pair, err := tls.LoadX509KeyPair(filepath.Join(server, "server.crt"), filepath.Join(server, "server.key"))
	if err != nil {
		return nil, err
	}
	pem, err := os.ReadFile(filepath.Join(repo, "agent", "transport", "connection", "ca.crt"))
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("agent CA file contains no certificate")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}, nil
}

func run(repo, scannerDir string) error {
	tlsConfig, err := serverTLS(repo)
	if err != nil {
		return err
	}
	grpcListener, err := net.Listen("tcp4", "127.0.0.1:6751")
	if err != nil {
		return err
	}
	defer grpcListener.Close()
	httpListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer httpListener.Close()
	origin := "http://" + httpListener.Addr().String()
	fixtureURL := origin + "/demo-fixture"
	demo := newDemo(fixtureURL)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /demo-fixture", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		if _, err := w.Write(demo.plugin); err == nil {
			log.Printf("event=fixture_download path=%s bytes=%d", r.URL.Path, len(demo.plugin))
		}
	})
	if scannerDir != "" {
		demo.scanner, err = loadScannerDemo(scannerDir, origin)
		if err != nil {
			return err
		}
		mux.HandleFunc("GET "+scannerPath, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/gzip")
			if _, err := w.Write(demo.scanner.archive); err == nil {
				log.Printf("event=scanner_download bytes=%d", len(demo.scanner.archive))
			}
		})
	}
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer httpServer.Close()
	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsConfig)))
	defer grpcServer.Stop()
	proto.RegisterTransferServer(grpcServer, demo)
	proto.RegisterFileExtServer(grpcServer, demo)
	serveError := make(chan error, 2)
	go func() { serveError <- httpServer.Serve(httpListener) }()
	go func() { serveError <- grpcServer.Serve(grpcListener) }()
	log.Printf("event=ready grpc=%s fixture_url=%s", grpcListener.Addr(), fixtureURL)
	return <-serveError
}

func main() {
	repo := flag.String("repo", "../../..", "path to the Elkeid repository")
	scannerDir := flag.String("scanner-fixture-dir", "", "directory containing the optional offline scanner fixture package and files")
	flag.Parse()
	if err := run(*repo, *scannerDir); err != nil {
		log.Fatal(err)
	}
}
