package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/catalinfl/adler"
)

//go:embed index.html
var indexHTML embed.FS

var guestCounter uint64

type clientEvent struct {
	Type    string `json:"type"`
	Name    string `json:"name,omitempty"`
	Room    string `json:"room,omitempty"`
	Message string `json:"message,omitempty"`
}

func main() {
	a := adler.New(
		adler.WithDispatchAsync(true),
		adler.WithMessageBufferSize(64),
		adler.WithPingPeriod(2),
		adler.WithDeleteRoomOnEmpty(false),
	)

	a.NewRoom("lobby")
	a.NewRoom("alpha")
	a.NewRoom("beta")

	a.HandleConnect(func(s *adler.Session) {
		name := strings.TrimSpace(s.Request().URL.Query().Get("name"))
		if name == "" {
			id := atomic.AddUint64(&guestCounter, 1)
			name = fmt.Sprintf("guest-%d", id)
		}

		s.Set("name", name)
		s.Set("room", "lobby")

		lobby, _ := a.Room("lobby")
		if err := lobby.Join(s); err != nil {
			log.Println("join room:", err)
			return
		}

		lobby.BroadcastJSON(adler.Map{
			"type":    "system",
			"message": fmt.Sprintf("%s joined the room", name),
			"room":    "lobby",
		})

		s.WriteJSON(adler.Map{
			"type": "room_changed",
			"room": "lobby",
		})
	})

	a.HandleDisconnect(func(s *adler.Session) {
		name, ok := s.GetString("name")
		if !ok {
			name = "unknown"
		}
		roomName, ok := s.GetString("room")
		if !ok {
			roomName = "lobby"
		}
		r, _ := a.Room(roomName)

		r.BroadcastJSON(adler.Map{
			"type":    "system",
			"message": fmt.Sprintf("%s left the room", name),
			"room":    roomName,
		})
	})

	a.HandleMessage(func(s *adler.Session, msg []byte) {
		currentRoomName, ok := s.GetString("room")
		if !ok || currentRoomName == "" {
			currentRoomName = "lobby"
		}
		currentRoom, _ := a.Room(currentRoomName)

		var event clientEvent
		if err := json.Unmarshal(msg, &event); err == nil && event.Type != "" {
			switch event.Type {
			case "rename":
				oldName, _ := s.GetString("name")
				newName := strings.TrimSpace(event.Name)
				if newName == "" || newName == oldName {
					return
				}

				s.Set("name", newName)

				currentRoom.BroadcastJSON(adler.Map{
					"type":    "system",
					"message": fmt.Sprintf("%s is now known as %s", oldName, newName),
					"room":    currentRoomName,
				})
				return
			case "join_room":
				targetRoomName := strings.TrimSpace(strings.ToLower(event.Room))
				targetRoom, _ := a.Room(targetRoomName)
				targetRoomName = targetRoom.Name()

				if targetRoomName == currentRoomName {
					return
				}

				name, ok := s.GetString("name")
				if !ok {
					name = "unknown"
				}

				if err := targetRoom.Join(s); err != nil {
					log.Println("join target room:", err)
					return
				}

				s.Set("room", targetRoomName)

				currentRoom.BroadcastJSON(adler.Map{
					"type":    "system",
					"message": fmt.Sprintf("%s moved to %s", name, targetRoomName),
					"room":    currentRoomName,
				})

				targetRoom.BroadcastJSON(adler.Map{
					"type":    "system",
					"message": fmt.Sprintf("%s joined %s", name, targetRoomName),
					"room":    targetRoomName,
				})

				s.WriteJSON(adler.Map{
					"type": "room_changed",
					"room": targetRoomName,
				})
				return
			case "chat":
				msg = []byte(event.Message)
			}
		}

		name, ok := s.GetString("name")
		if !ok {
			name = "unknown"
		}

		currentRoom.BroadcastJSON(adler.Map{
			"type":    "chat",
			"from":    name,
			"message": string(msg),
			"room":    currentRoomName,
		})
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := indexHTML.ReadFile("index.html")
		if err != nil {
			http.Error(w, "index not found", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if err := a.HandleRequest(w, r); err != nil {
			log.Println("handle websocket:", err)
		}
	})

	log.Println("chat running on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
