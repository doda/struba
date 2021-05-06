// Package main implements a server for Completor service.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"

	pb "completor/completor"
	"completor/zkutils"

	"github.com/go-zookeeper/zk"
	"google.golang.org/grpc"
)

var sharedTrie = &Trie{}

type server struct {
	pb.UnimplementedCompletorServer
	zkconn    *zk.Conn
	znodePath string
	info      *zkutils.NodeInfo
}

func newNodeInfo() *zkutils.NodeInfo {
	return &zkutils.NodeInfo{
		HostName:   os.Getenv("HOSTNAME"),
		Port:       os.Getenv("PORT"),
		RangeStart: os.Getenv("RANGE_START"),
		RangeEnd:   os.Getenv("RANGE_END"),
		Version:    0,
	}
}

func (s *server) createEphemeralNode(c *zk.Conn) error {
	zkutils.EnsurePath(s.zkconn, "/struba/backend/nodes")
	data := s.serializeInfo()
	znodePath, err := c.Create("/struba/backend/nodes/", data, zk.FlagEphemeral|zk.FlagSequence, zk.WorldACL(zk.PermAll))
	if err != nil {
		log.Printf("Create ephemeral path with error(%+v)\n", err)
		return err
	}
	log.Printf("Created ephemeral_path: %+v\n", znodePath)
	s.znodePath = znodePath
	return nil
}

func (s *server) serializeInfo() []byte {
	jsonNodeInfo, _ := json.Marshal(s.info)
	return []byte(jsonNodeInfo)
}

func (s *server) BuildTrie(ctx context.Context, in *pb.BuildTrieRequest) (*pb.BuildTrieResponse, error) {
	log.Println("Got BuildTrie RPC")
	sharedTrie = buildTrie(s.info.RangeStart, s.info.RangeEnd)
	// Update this node's info in ZK
	s.info.Version = int(in.Version)
	data := s.serializeInfo()
	log.Println("Building Trie for version", in.Version)

	_, stat, err := s.zkconn.Get(s.znodePath)
	if err != nil {
		panic(err)
	}
	log.Println("Updating info at ", s.znodePath, in.Version)
	log.Println(string(data))
	_, err = s.zkconn.Set(s.znodePath, data, stat.Version)
	if err != nil {
		log.Println("Failed to update node info")
		log.Println(err)
	}
	return &pb.BuildTrieResponse{}, nil
}

func (s *server) AutoComplete(ctx context.Context, in *pb.AutoCompleteRequest) (*pb.AutoCompleteResponse, error) {
	var results []string
	log.Printf("Received: %v", in.GetQuery())
	node := findNode(sharedTrie.root, []rune(in.GetQuery()))

	if node != nil {
		phraseInfo, ok := node.Meta.(PhraseInfo)
		if ok {
			results = phraseInfo.Phrases
		}
	}

	return &pb.AutoCompleteResponse{Results: results}, nil
}

func main() {
	c := zkutils.Connect()
	defer c.Close()

	info := newNodeInfo()
	server := &server{zkconn: c, info: info}
	server.createEphemeralNode(c)

	// Ready to serve
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", info.Port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	pb.RegisterCompletorServer(s, server)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
