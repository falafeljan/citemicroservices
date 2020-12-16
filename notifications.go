package main

import (
	"encoding/json"
	"fmt"
	"github.com/dgraph-io/badger"
	uuid "github.com/google/uuid"
	"github.com/gorilla/mux"
	"net/http"
)

type Notification struct {
	ID      string `json:"id"`
	Actor   string `json:"actor"`
	Object  string `json:"object"`
	Target  string `json:"target"`
	Updated string `json:"updated"`
}

type LDNInbox struct {
	Context  string   `json:"@context"`
	ID       string   `json:"@id"`
	Contains []string `json:"contains"`
}

type LDNotification struct {
	Context string `json:"@context"`
	ID      string `json:"@id"`
	Type    string `json:"@type"`
	Actor   string `json:"actor"`
	Object  string `json:"object"`
	Target  string `json:"target"`
	Updated string `json:"updated"`
}

const maxResponseSize = 128

func getInbox(inboxID string) ([]Notification, error) {
	notifications := make([]Notification, 0, maxResponseSize)
	err := db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := []byte(fmt.Sprintf("%s-", inboxID))
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(v []byte) error {
				var notification Notification
				err := json.Unmarshal(v, &notification)
				if err != nil {
					return err
				}
				notifications = append(notifications, notification)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return notifications, nil
}

func getNotification(inboxID string, notificationID string) (Notification, error) {
	var notification Notification
	err := db.View(func(txn *badger.Txn) error {
		id := fmt.Sprintf("%s-%s", inboxID, notificationID)
		item, err := txn.Get([]byte(id))
		if err != nil {
			return err
		}

		err = item.Value(func(v []byte) error {
			return json.Unmarshal(v, &notification)
		})
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Notification{}, nil
	}

	return notification, nil
}

func createNotification(inboxID string, notification Notification) (Notification, error) {
	notification.ID = uuid.New().String()

	n, err := json.Marshal(&notification)
	if err != nil {
		return Notification{}, err
	}

	err = db.Update(func(txn *badger.Txn) error {
		id := fmt.Sprintf("%s-%s", inboxID, notification.ID)
		e := badger.NewEntry([]byte(id), n)
		err := txn.SetEntry(e)
		return err
	})
	if err != nil {
		return Notification{}, err
	}

	return Notification{}, nil
}

type stringTransform = func(string) string

func mapNotificationsID(ns []Notification, transform stringTransform) []string {
	ids := make([]string, len(ns))
	for i, n := range ns {
		ids[i] = transform(n.ID)
	}
	return ids
}

func makeLDNInbox(inboxID string, makeID stringTransform, ns []Notification) LDNInbox {
	return LDNInbox{
		Context:  "http://www.w3.org/ns/ldp",
		ID:       inboxID,
		Contains: mapNotificationsID(ns, makeID),
	}
}

func makeLDNotification(makeID stringTransform, n Notification) LDNotification {
	return LDNotification{
		Context: "https://www.w3.org/ns/activitystreams",
		ID:      makeID(n.ID),
		Type:    "Announce",
		Actor:   n.Actor,
		Object:  n.Object,
		Target:  n.Target,
		Updated: n.Updated,
	}
}

func handleInbox(w http.ResponseWriter, r *http.Request) {
	// FIXME: validate URN to reference an entire work
	vars := mux.Vars(r)
	inboxURN := vars["URN"]

	if r.Method == http.MethodPost {
		var n Notification

		err := json.NewDecoder(r.Body).Decode(&n)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		notification, err := createNotification(inboxURN, n)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("content-type", "application/ld+json")
		w.Header().Set("location", fmt.Sprintf("http://%s/texts/%s/inbox/%s", r.Host, inboxURN, notification.ID))
		w.WriteHeader(201)
	} else if r.Method == http.MethodGet {
		// TODO: Get notifications
		notifications, err := getInbox(inboxURN)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		inboxID := fmt.Sprintf("http://%s/texts/%s/inbox", r.Host, inboxURN)
		inbox := makeLDNInbox(inboxID, func(id string) string {
			return fmt.Sprintf("%s/%s", inboxID, id)
		}, notifications)
		w.Header().Set("content-type", "application/ld+json")
		json.NewEncoder(w).Encode(inbox)
	} else {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
}

func handleNotification(w http.ResponseWriter, r *http.Request) {
	// FIXME: validate URN to reference either fully-qualified (passage) or an entire work

	vars := mux.Vars(r)
	inboxURN := vars["URN"]
	notificationID := vars["ID"]

	if r.Method != http.MethodGet {
		w.WriteHeader(404)
		w.Write([]byte("Not Found"))
	}

	notification, err := getNotification(inboxURN, notificationID)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	n := makeLDNotification(func(id string) string {
		return fmt.Sprintf("http://%s/texts/%s/inbox/%s", r.Host, inboxURN, id)
	}, notification)
	w.Header().Set("content-type", "application/ld+json")
	json.NewEncoder(w).Encode(n)
}
