package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type AlertDoc struct {
	RuleID         string    `json:"rule_id"`
	RuleName       string    `json:"rule_name"`
	AlertID        string    `json:"alert_id"`
	ContextMessage string    `json:"context_message"`
	Timestamp      time.Time `json:"@timestamp"`
	TSend          string    `json:"tsend"`
}

type SearchResponse struct {
	Hits struct {
		Hits []struct {
			ID     string   `json:"_id"`
			Source AlertDoc `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

func main() {
	// Configuration (In a real app, these should come from env vars or config file)
	esURL := os.Getenv("ES_URL")
	if esURL == "" {
		esURL = "http://localhost:9200"
	}
	esIndex := os.Getenv("ES_INDEX")
	if esIndex == "" {
		esIndex = "telegaalert"
	}
	esAPIKey := os.Getenv("ES_API_KEY")
	tgToken := os.Getenv("TG_TOKEN")
	// For demonstration, we'll try to get them, but handle missing values gracefully

	if tgToken == "" {
		log.Println("Warning: TG_TOKEN not set. Telegram alerts will be skipped.")
	}

	// Initialize Elasticsearch client
	cfg := elasticsearch.Config{
		Addresses: []string{esURL},
		APIKey:    esAPIKey,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	es, err := elasticsearch.NewClient(cfg)
	if err != nil {
		log.Fatalf("Error creating the ES client: %s", err)
	}

	// Initialize Telegram client
	var bot *tgbotapi.BotAPI
	if tgToken != "" {
		bot, err = tgbotapi.NewBotAPI(tgToken)
		if err != nil {
			log.Printf("Error creating Telegram bot: %s", err)
		}
	}

	ticker := time.NewTicker(30 * time.Second)
	log.Println("Starting alert poller...")

	for {
		select {
		case <-ticker.C:
			processAlerts(es, esIndex, bot)
		}
	}
}

func processAlerts(es *elasticsearch.Client, index string, bot *tgbotapi.BotAPI) {
	ctx := context.Background()

	// 1. Search for documents where tsend is "false"
	query := map[string]any{
		"query": map[string]any{
			"term": map[string]any{
				"tsend": "false",
			},
		},
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		log.Printf("Error encoding query: %s", err)
		return
	}

	res, err := es.Search(
		es.Search.WithContext(ctx),
		es.Search.WithIndex(index),
		es.Search.WithBody(&buf),
		es.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		log.Printf("Error searching documents: %s", err)
		return
	}
	defer res.Body.Close()

	if res.IsError() {
		log.Printf("Error search response: %s", res.String())
		return
	}

	var r SearchResponse
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		log.Printf("Error parsing search response: %s", err)
		return
	}

	for _, hit := range r.Hits.Hits {
		doc := hit.Source
		log.Printf("Processing alert: %s", hit.ID)

		// 2. Send Telegram alert
		if bot != nil {
			// Get Chat ID from env or somewhere. If not set, we can't send.
			// In this example, I'll assume we want to send it.
			// Ideally, the chat_id might be in the document or config.
			chatID := new(int64)
			// For now, let's just log it if chat ID is missing
			fmt.Sscanf(os.Getenv("TG_CHAT_ID"), "%d", chatID)

			if *chatID != 0 {
				msgText := fmt.Sprintf("🚨 *Alert: %s*\n\n%s\n\nRule: %s\nTime: %s",
					doc.AlertID, doc.ContextMessage, doc.RuleName, doc.Timestamp.Format(time.RFC3339))
				msg := tgbotapi.NewMessage(*chatID, msgText)
				msg.ParseMode = "Markdown"
				_, err := bot.Send(msg)
				if err != nil {
					log.Printf("Error sending Telegram message: %s", err)
					continue // Don't update ES if notification failed? Or maybe we should?
				}
			} else {
				log.Println("TG_CHAT_ID not set, skipping Telegram notification.")
			}
		}

		// 3. Update tsend to "true"
		updateDoc := map[string]any{
			"doc": map[string]any{
				"tsend": "true",
			},
		}
		var upBuf bytes.Buffer
		json.NewEncoder(&upBuf).Encode(updateDoc)

		req := esapi.UpdateRequest{
			Index:      index,
			DocumentID: hit.ID,
			Body:       &upBuf,
			Refresh:    "true",
		}

		upRes, err := req.Do(ctx, es)
		if err != nil {
			log.Printf("Error updating document %s: %s", hit.ID, err)
			continue
		}
		defer upRes.Body.Close()

		if upRes.IsError() {
			log.Printf("Error updating document response: %s", upRes.String())
		} else {
			log.Printf("Successfully processed and updated alert %s", hit.ID)
		}
	}
}
