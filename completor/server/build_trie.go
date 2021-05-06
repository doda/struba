// Package main implements a server for Completor service.
package main

import (
	"log"
	"os"

	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/ClickHouse/clickhouse-go"
)

const TOP_K = 5

func SearchInt64s(a []uint64, x uint64) int {
	return sort.Search(len(a), func(i int) bool { return a[i] >= x })
}

type PhraseInfo struct {
	Phrases []string
	Counts  []uint64
}

func addToTrie(t *Trie, phrase string, count uint64) {
	node := t.Add(phrase, PhraseInfo{})
	for node := node.Parent(); node != nil; {
		meta := node.Meta

		var pInfo PhraseInfo
		switch pCast := meta.(type) {
		case nil:
			pInfo = PhraseInfo{}
		case PhraseInfo:
			pInfo = pCast
		}

		counts := pInfo.Counts
		phrases := pInfo.Phrases

		i := SearchInt64s(counts, count)

		counts = append(counts, 0)
		copy(counts[i+1:], counts[i:])
		counts[i] = count
		if len(counts) > TOP_K {
			counts = counts[1:]
		}

		phrases = append(phrases, "")
		copy(phrases[i+1:], phrases[i:])
		phrases[i] = phrase
		if len(phrases) > TOP_K {
			phrases = phrases[1:]
		}

		node.Meta = PhraseInfo{
			Phrases: phrases,
			Counts:  counts,
		}

		node = node.Parent()
	}
}

func connectCH() *sql.DB {
	os.Getenv("CH_HOST")
	chHost := os.Getenv("CH_HOST")
	if chHost == "" {
		chHost = "clickhouse"
	}
	chPort := os.Getenv("CH_PORT")
	if chPort == "" {
		chPort = "9000"
	}
	connect, err := sql.Open("clickhouse", fmt.Sprintf("tcp://%s:%s?debug=true", chHost, chPort))
	if err != nil {
		log.Fatal(err)
	}
	if err := connect.Ping(); err != nil {
		if exception, ok := err.(*clickhouse.Exception); ok {
			log.Printf("[%d] %s \n%s\n", exception.Code, exception.Message, exception.StackTrace)
		} else {
			log.Println(err)
		}
		return nil
	}
	return connect
}

func buildTrie(rangeStart string, rangeEnd string) *Trie {
	t := New()
	connect := connectCH()
	rows, err := connect.Query(`
SELECT
    toStartOfHour(Created) as Created,
    Phrase,
    sum(Count) as Count
FROM
    kafka_phrases_1m
WHERE
    Phrase >= ?
    AND Phrase < ?
    AND Created > now() - INTERVAL 3 DAY
GROUP BY
    Created,
    Phrase
ORDER BY
    Phrase
;`, rangeStart, rangeEnd)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			created time.Time
			phrase  string
			count   uint64
		)
		if err := rows.Scan(&created, &phrase, &count); err != nil {
			log.Fatal(err)
		}
		addToTrie(t, phrase, count)
	}

	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}

	return t
}
