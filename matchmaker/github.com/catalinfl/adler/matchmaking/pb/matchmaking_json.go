package pb

import "encoding/json"

func (x *QueueStatus) MarshalJSON() ([]byte, error) {
	switch p := x.Payload.(type) {
	case *QueueStatus_QueueJoined:
		return json.Marshal(map[string]string{
			"type":    "queue_joined",
			"message": p.QueueJoined.Message,
		})
	case *QueueStatus_WaitQueueJoined:
		return json.Marshal(map[string]string{
			"type":    "wait_queue_joined",
			"message": p.WaitQueueJoined.Message,
		})
	case *QueueStatus_PromotedToQueue:
		return json.Marshal(map[string]string{
			"type":    "promoted_to_queue",
			"message": p.PromotedToQueue.Message,
		})
	case *QueueStatus_QueueError:
		return json.Marshal(map[string]string{
			"type":    "queue_error",
			"message": p.QueueError.Message,
		})
	case *QueueStatus_MatchFound:
		return json.Marshal(map[string]any{
			"type":    "match_found",
			"room_id": p.MatchFound.RoomId,
			"players": p.MatchFound.Players,
		})
	default:
		return json.Marshal(map[string]string{"type": "unknown"})
	}
}
