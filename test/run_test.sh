#!/bin/bash
set -e

# Start the containers
echo "Starting containers..."
docker-compose up -d

# Wait for Home Assistant to fully start
echo "Waiting for Home Assistant to start (this may take a minute)..."
while ! curl -s http://localhost:18124/api/ > /dev/null; do
  sleep 5
  echo "Still waiting for Home Assistant..."
done

echo "Home Assistant is ready!"
echo "Access the UI at: http://localhost:18124"
echo "Default username: dev@example.com"
echo "Default password: welcome123"
echo ""

# Set the token for hass2ch
export HASS_TOKEN="eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJkZXZfdG9rZW4iLCJpYXQiOjE2NzYwNDg5MjAsImV4cCI6MTk5MTQwODkyMH0.jzcIVCXYUJZnS178bWpTQ-8wp-xBpgUkHSJkP8Bos3A"

echo "To test hass2ch with local Home Assistant:"
echo "export HASS_TOKEN=\"$HASS_TOKEN\""
echo "go run cmd/hass2ch/main.go --pretty-log --host localhost:18124 dump"
echo ""
echo "Or run the full pipeline with:"
echo "go run cmd/hass2ch/main.go --pretty-log --host localhost:18124 pipeline"
echo ""
echo "To run the reconnection test:"
echo "HASS_HOST=localhost:18124 HASS_TOKEN=\"$HASS_TOKEN\" go test -v ./test/..."
echo ""
echo "Press Ctrl+C to stop the containers"

# Keep the script running until user presses Ctrl+C
read -r -d '' _ </dev/tty