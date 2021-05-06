
# Struba

1. Boot up the cluster

```
$ docker-compose up
```

2. Set up tables

```
clickhouse-client --host localhost --port 8999 -mn < logput/tables.sql
```

3. Insert data

```
docker-compose run logput
```

Don't leave this running for too long, a few seconds should be enough! (Ctrl+C)

4. Query the front-end

```
$ curl http://localhost:3200/complete?q=A

{"results":["Agustina Blanda","Afton Deckow","Aaliyah Reynolds","Alpha King Pale Ale","Arrogant Bastard Ale"]}
```



# The How and Why

Struba is a fun little side-project I've been working on to learn more about the following areas:
* Go
* Distributed systems
* Custom data structures (Tries in this case)
* Sharding
* Kafka
* ClickHouse
* Service Discovery

The full source code is available on [GitHub](https://www.github.com/dodafin/struba).

The problem to be solved is a seemingly simple one: Type-ahead search a.k.a. Auto-Complete: 
![](https://i.imgur.com/YGyr7Cc.png)

That is for the query 
```
$ curl http://localhost:3200/complete?q=A
```
we want to return results like:
```
{"results":["Agustina Blanda","Afton Deckow","Aaliyah Reynolds","Alpha King Pale Ale","Arrogant Bastard Ale"]}
```

## Trie

![](https://upload.wikimedia.org/wikipedia/commons/b/be/Trie_example.svg)

The fundamental data structure helping us serve this query is the [trie](https://en.wikipedia.org/wiki/Trie),  an efficient way of storing and doing prefix-searches on strings.

A bog-standard trie is used with one modification: Each node apart from storing it's letter and pointers to it's children also stores the top 5 most popular phrases that are stored in its children. This means that our lookup to serve a query is dead-simple: Walk the tree character by character, and spit out the 5 results stored at the node.

I actually managed to build a 1-node version of this trie that (without updates) in an afternoon. But throw in sharding and continuously updating data via trie re-builds and suddenly complexity increases manifold :)

## Architecture overview:


Next we'll zoom into the different parts that come together to help us answer life's questions:

![](https://i.imgur.com/hhRtz4G.png)

## <span style="color:MediumTurquoise">Ingest</span>: How does data make it from the user into ClickHouse?

The design for the ingest pipe of the system is heavily inspired by how CloudFlare uses Kafka and ClickHouse to do log analytics at scale (millions of queries per second): [[1]](https://blog.cloudflare.com/scaling-out-postgresql-for-cloudflare-analytics-using-citusdb/), 
[[2]](https://blog.cloudflare.com/how-cloudflare-analyzes-1m-dns-queries-per-second/), 
[[3]](https://blog.cloudflare.com/http-analytics-for-6m-requests-per-second-using-clickhouse/).

Maybe a bit overkill for this type of application, but I was fascinated with how a simple setup like Kafka + ClickHouse could offer such incredible performance.

**1. Kafka**

The source contains an example application `logput.go` that inserts randomly generated search query logs into the Kafka `phrases-json` topic. As a nod to Kafka's performance we're already able to consume ~500k logs per second in this manner (from a single goroutine).

**2. Kafka -> ClickHouse**

Next we're using ClickHouse's Kafka Engine which allows us to subscribe to our Kafka topic from above:

```
CREATE TABLE kafka_phrases_stream (
    Created DateTime,
    Count UInt64,
    Phrase String
) ENGINE = Kafka(
    'kafka:9092',
    'phrases-json',
    'ch-phrases-group',
    'JSONEachRow'
);
```
ClickHouse is a bit peculiar in how it likes us to consume from our newly created Kafka-Engine table:

First we'll create the table that'll wind up holding all records that were streamed into Kafka. All aggregations (in our case 1) will source from this table.

```
CREATE TABLE kafka_phrases AS kafka_phrases_stream ENGINE = MergeTree() PARTITION BY toYYYYMM(Created)
ORDER BY
    Created;
```

And next we can hook up our `kafka_phrases_stream` to our `kafka_phrases` table via a `kafka_phrases_consumer`:

```
CREATE MATERIALIZED VIEW kafka_phrases_consumer TO kafka_phrases AS
SELECT
    *
FROM
    kafka_phrases_stream;
```
Done! Now everything streamed into the Kafka topic will also wind up in ClickHouse.


**3. ClickHouse Aggregation**

Materialized views are super-popular in ClickHouse and allow us have an up-to-date and space-efficient view of our search logs:

```
CREATE MATERIALIZED VIEW kafka_phrases_1m ENGINE = SummingMergeTree() PARTITION BY toYYYYMM(Created)
ORDER BY
    (Created, Phrase) POPULATE AS
SELECT
    toStartOfMinute(Created) as Created,
    Phrase,
    sum(Count) as Count
FROM
    kafka_phrases
GROUP BY
    Created,
    Phrase;
```
This means that if our fact table receives the rows:

```
┌─────────────Created─┬─Count─┬─Phrase────────────┐
│ 2021-05-06 12:41:55 │     1 │ Richmond Dietrich │
│ 2021-05-06 12:41:55 │     1 │ Elmer Nienow      │
│ 2021-05-06 12:41:55 │     1 │ Willa Lang        │
│ 2021-05-06 12:41:55 │     1 │ Willa Lang        │
│ 2021-05-06 12:41:55 │     1 │ Willa Lang        │
└─────────────────────┴───────┴───────────────────┘
```

Our 1-minute aggregated materialized view will automatically sum them and store them as:
```
┌─────────────Created─┬─Count─┬─Phrase────────────┐
│ 2021-05-06 12:41:00 │     1 │ Richmond Dietrich │
│ 2021-05-06 12:41:00 │     1 │ Elmer Nienow      │
│ 2021-05-06 12:41:00 │     3 │ Willa Lang        │
└─────────────────────┴───────┴───────────────────┘
```

One thing to note about the setup as described here is that we're leaving rows lying around in our fact table after we're done aggregating them. One way we could ameliorate this is by having [ClickHouse move data to a different storage tier past some TTL](https://clickhouse.tech/docs/en/engines/table-engines/mergetree-family/mergetree/#table_engine-mergetree-multiple-volumes).

Another thing is that we could dial down the granularity of our `GROUP BY` (to group by hour or by day instead of by minute), to increase the row-reduction factor of our aggregated table.

## <span style="color:RoyalBlue">Trie Build</span>: How are backend nodes built?

Now that we have an efficient way of aggregating and storing our raw data, we'll need an efficient way of serving it to our users.

All nodes are partitioned into "blue" and "green" nodes based on their ID (a simple modulo 2). Based on this node color we determine whether a node is "active" or on "stand-by": Nodes with the same "color" (or modulo 2) as the current version are "active" and the others are "stand-by".

![](https://i.imgur.com/P6dXPrv.png)

Backend nodes do not update their internal trie as new data flows into the system, but rather they are periodically re-built by a "Trie Build Coordinator" which instructs the stand-by nodes via gRPC to build a trie for a new version of our trie data. The nodes then independently query ClickHouse for the most up-to-date data.

In this example if the current trie Trie Version is 3, all stand-by nodes will be instructed to build a trie for version 4. Once all nodes have returned successfully ("trie built successfully"), the current version is updated to 4, which will cause the frontend to route traffic to the nodes that have now switched from "stand-by" to "active", since their "color" is the same as the currently active Trie Version.

Afterwards the system state will be:

![](https://i.imgur.com/UXUHNNU.png)

## <span style="color:LightSalmon">Serve</span>: How are auto-complete queries served?


Now that the hard work of provisioning ready-to-serve backend nodes is done, fulfilling user queries such as `/complete?q=kim kardas` is rather easy:

The frontend node asks ZooKeeper for current node information and selects all backend nodes that are serving queries for the current Trie Version and handling the range the current query falls into. It then iterates through them in random order to try and fulfill the user request (i.e. load balances across them).

![](https://i.imgur.com/7iny71P.png)

No replication is included in this example (or the `docker-compose.yml` that the repo ships with), but the front-end will automatically load-balance across all nodes that fulfill the above conditions, so one simply needs to provision more nodes with the same partition range assigned to enable replication in the system.



## ZooKeeper

ZooKeeper is perhaps not the first choice for a Go developer (etcd and Consul are other choices), but it came free with Kafka and ClickHouse depends on it for replication, so it made sense to make use of it for our sharding efforts.

ZooKeeper stores 2 kinds of information for us:

* `/v_current`: The currently active "Trie Version" of trie data that we're serving up. 
* `/backend/nodes/{id}`: A list of backend nodes connected to ZK with meta-data:
    * Hostname / port information (how to connect to the node)
    * Partition range information (which range of phrases the node is responsible for)
    * Current Trie Version of the node


## Areas to improve:

* Stand-by nodes retain a full copy of the trie in RAM but don't do anything with them. This could be fine if we always wanted to be able to roll back to the previous trie version or they could free the memory once they turn into stand-by nodes (by watching for relevant changes from ZK).
* Just a single front-end instance is included in the current implementation, but these would be easy to load-balance across (since they are just stateless JSON-over-HTTP go servers).
* The trie stores all "top 5 phrases" on each node, this can be made more memory-efficient.
* Kafka logs are stored as JSON, for optimal performance a format like Cap'n Proto would likely be a better fit.
* Node IDs are assigned via ZooKeeper ephemeral, sequential IDs, which do not guarantee a gap-less sequence, which means we could wind up having 0 nodes in our "blue" or "green" pool, though this becomes very unlikely as we scale up the # of nodes. Assigning IDs in a different way could guarantee an evenly sized split, but I found it more elegant to have nodes simply join the ZK pool and have their ID assigned that way.
* Partition ranges are assigned manually but it'd not be too hard to have the Trie Build Coordinator calculate appropriate ranges when building. This would also allow the system to automatically scale to a higher degree of "shardedness" by throwing more nodes in the pool.
* The frontend queries ZooKeeper for information on all nodes on every request. If this becomes a bottleneck, it could be optimized via e.g. Apache Curator's cache pattern (using ZK's watch feature and pulling the new config down once it's updated) or simply implementing a cache with a very short TTL (1 second) locally.
* Top phrases are ranked by their number of occurences in the 3 days, equally weighted across time (3 days ago is as relevant as 3 seconds ago). This can be changed by implementing something akin to an "Exponential Smoothing Average" that assigns more recent searches a higher weight.
* For the source code: I'm a Go beginner, error handling is minimal, and the config management / setup could be improved. :)

## Closing notes:

I had a lot of fun building this, and learned a lot, especially about ZooKeeper and how it's atomic primitives can be used to build and coordinate a distributed system. Especially interesting to see was how simple it was to build something that "runs" locally and how much more complex everything became once tries had to be sharded and updated via something akin to a blue-green deployment. Whew!

