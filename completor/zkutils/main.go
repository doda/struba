package zkutils

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-zookeeper/zk"
)

type NodeInfo struct {
	HostName   string
	Port       string
	RangeStart string
	RangeEnd   string
	Version    int
}

// Creates all the parent znodes for a given path, similar to mkdir -p
func EnsurePath(c *zk.Conn, path string) {
	parts := strings.Split(path, "/")
	for i := range parts {
		if i == 0 {
			continue
		}
		CreateIfNotExists(c, strings.Join(parts[:i+1], "/"))
	}
}

func IDFromPath(path string) int {
	chunks := strings.Split(path, "/")
	id, _ := strconv.Atoi(chunks[len(chunks)-1])
	return id
}

func CreateIfNotExists(c *zk.Conn, path string) {
	if len(strings.Trim(path, "/")) == 0 {
		return
	}
	if exists, _, _ := c.Exists(path); !exists {
		_, err := c.Create(path, []byte(""), 0, zk.WorldACL(zk.PermAll))
		if err != nil {
			panic(err)
		}
	}
}

func GetNodeInfos(c *zk.Conn, path string) map[string]*NodeInfo {
	result := make(map[string]*NodeInfo)
	children, _, _ := c.Children(path)
	fmt.Println("Children:", children)
	for _, childID := range children {
		nodeInfo := &NodeInfo{}
		childPath := path + "/" + childID
		jsonNodeInfo, _, _ := c.Get(childPath)
		json.Unmarshal(jsonNodeInfo, &nodeInfo)
		result[childPath] = nodeInfo
	}
	return result
}

// which color is this ID, red or green?
func IDColor(id int) string {
	if id%2 == 0 {
		return "red"
	}
	return "green"
}

func Connect() *zk.Conn {
	zkHost := os.Getenv("ZK_HOST")
	if zkHost == "" {
		zkHost = "127.0.0.1"
	}

	c, _, err := zk.Connect([]string{zkHost}, time.Second)
	if err != nil {
		panic(err)
	}
	return c
}
