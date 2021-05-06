package main

import (
	"encoding/json"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"time"

	. "github.com/Shopify/sarama"

	"github.com/brianvoe/gofakeit/v6"
)

type PhrasePayload struct {
	Phrase  string
	Count   uint32
	Created int64
}

func main() {
	config := NewConfig()
	config.ClientID = "go-kafka-consumer"
	config.Consumer.Return.Errors = true

	config.Producer.Return.Successes = true
	producer, err := NewAsyncProducer([]string{"kafka:9092"}, config)
	if err != nil {
		panic(err)
	}

	// Trap SIGINT to trigger a graceful shutdown.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)

	var (
		wg                                  sync.WaitGroup
		enqueued, successes, producerErrors int
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range producer.Successes() {
			successes++
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for err := range producer.Errors() {
			log.Println(err)
			producerErrors++
		}
	}()

	faker := gofakeit.New(0)
	log.Println("Submitting logs...")
ProducerLoop:
	for {
		var phrase string
		if rand.Float32() > 0.9 {
			phrase = faker.BeerName()
		} else {
			phrase = faker.Name()
		}

		exPhrase := &PhrasePayload{
			Phrase:  phrase,
			Count:   1,
			Created: time.Now().Unix()}
		exPharseJson, _ := json.Marshal(exPhrase)

		message := &ProducerMessage{Topic: "phrases-json", Value: StringEncoder(exPharseJson)}
		select {
		case producer.Input() <- message:
			enqueued++

		case <-signals:
			producer.AsyncClose() // Trigger a shutdown of the producer.
			break ProducerLoop
		}
	}

	wg.Wait()

	log.Printf("Successfully produced: %d; errors: %d\n", successes, producerErrors)
}
