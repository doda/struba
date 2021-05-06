package main

import (
	pb "completor/completor"
	"completor/zkutils"
	"context"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"time"

	"github.com/go-zookeeper/zk"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"google.golang.org/grpc"
)

func GetNodes(c *zk.Conn, query string) []*zkutils.NodeInfo {
	currentVersion_, _, _ := c.Get("/struba/v_current")
	currentVersion, _ := strconv.Atoi(string(currentVersion_))

	nodeInfos := zkutils.GetNodeInfos(c, "/struba/backend/nodes")
	result := []*zkutils.NodeInfo{}
	log.Println("Found nodes:", nodeInfos)
	for _, nodeInfo := range nodeInfos {
		if nodeInfo.Version == currentVersion && nodeInfo.RangeStart <= query && query < nodeInfo.RangeEnd {
			result = append(result, nodeInfo)
		}
	}

	return result
}

func main() {
	zkconn := zkutils.Connect()
	defer zkconn.Close()

	app := fiber.New()
	app.Use(logger.New())

	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("Hello, World 👋!")
	})

	app.Get("/complete", func(c *fiber.Ctx) error {
		query := c.Query("q")

		nodes := GetNodes(zkconn, query)
		// Pick random nodes to load balance across
		r := rand.New(rand.NewSource(time.Now().Unix()))
		log.Printf("Picked %d nodes:", len(nodes))
		for _, i := range r.Perm(len(nodes)) {
			node := nodes[i]

			address := fmt.Sprintf("%s:%s", node.HostName, node.Port)
			conn, err := grpc.Dial(address, grpc.WithInsecure(), grpc.WithBlock())
			log.Println("Connecting to", address)
			if err != nil {
				log.Fatalf("Did not connect: %v", err)
			}
			defer conn.Close()
			client := pb.NewCompletorClient(conn)

			req := &pb.AutoCompleteRequest{Query: query}
			res, err := client.AutoComplete(context.Background(), req)
			if err == nil {
				log.Println(res)
				return c.Status(fiber.StatusOK).JSON(fiber.Map{
					"results": res.Results,
				})
			}
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Could not connect to any node",
		})
	})

	app.Listen(":3200")
}
