package main

import (
	"context"
	"fmt"
	"log"
	"strconv"

	pb "completor/completor"
	"completor/zkutils"

	"google.golang.org/grpc"
)

func main() {
	c := zkutils.Connect()
	defer c.Close()

	zkutils.EnsurePath(c, "/struba")
	currentPath := "/struba/v_current"
	zkutils.CreateIfNotExists(c, currentPath)

	res, stats, err := c.Get(currentPath)
	if err != nil {
		panic(err)
	}
	currentBuild, _ := strconv.Atoi(string(res))

	stagingBuild := currentBuild + 1
	stagingColor := zkutils.IDColor(stagingBuild)
	log.Println("Building Tries for ID", stagingBuild, stagingColor)

	nodeInfos := zkutils.GetNodeInfos(c, "/struba/backend/nodes")
	errored := 0

	log.Println("Nodes:", nodeInfos)

	for childPath, nodeInfo := range nodeInfos {
		// We only build tries on nodes in standby mode (i.e. green if current ID is red)
		nodeID := zkutils.IDFromPath(childPath)
		log.Println("Evaluating", childPath, nodeID, "color", zkutils.IDColor(nodeID))
		if zkutils.IDColor(nodeID) != stagingColor {
			log.Println("Skipping...")
			continue
		}

		address := fmt.Sprintf("%s:%s", nodeInfo.HostName, nodeInfo.Port)
		log.Println("Connecting to", address, "nodeInfo:", nodeInfo)
		conn, err := grpc.Dial(address, grpc.WithInsecure(), grpc.WithBlock())
		if err != nil {
			log.Fatalf("did not connect: %v", err)
		}
		defer conn.Close()
		client := pb.NewCompletorClient(conn)
		req := &pb.BuildTrieRequest{Version: int32(stagingBuild)}
		_, err = client.BuildTrie(context.Background(), req)

		if err != nil {
			errored += 1
		}
	}

	if errored > 0 {
		log.Fatalf("Error building tries on some nodes")
	}
	// Switch over to serve requests from newly trie nodes
	c.Set(currentPath, []byte(strconv.Itoa(stagingBuild)), stats.Version)
}
