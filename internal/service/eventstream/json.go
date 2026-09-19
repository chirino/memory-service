package eventstream

import (
	"bytes"
	"encoding/json"
)

// MarshalDeliveryJSON encodes event-stream payloads without HTML escaping.
// Event streams are not embedded in HTML, and escaping <, >, and & can expand
// otherwise valid resources to six times their stored size.
func MarshalDeliveryJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}
