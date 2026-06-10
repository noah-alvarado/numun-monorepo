package email

import "encoding/json"

// MarshalEnqueue serializes an EnqueueRequest for SQS transport. Centralized
// so the worker decoder always matches the producer.
func MarshalEnqueue(r EnqueueRequest) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UnmarshalEnqueue is the worker-side counterpart.
func UnmarshalEnqueue(s string) (EnqueueRequest, error) {
	var r EnqueueRequest
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return r, err
	}
	return r, nil
}
