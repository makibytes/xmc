#!/bin/bash
set -e

API_KEY="${OPENCODE_API_KEY}"
URL="https://opencode.ai/zen/v1/chat/completions"
MODEL="deepseek-v4-flash-free"

if [ -z "$API_KEY" ]; then
    echo "Error: OPENCODE_API_KEY is not set" >&2
    exit 1
fi

if [ $# -eq 0 ]; then
    echo "Usage: $0 <question>" >&2
    exit 1
fi

QUESTION="$*"

BODY=$(jq -nc --arg q "$QUESTION" --arg m "$MODEL" '{
    model: $m,
    messages: [{role: "user", content: $q}],
    max_tokens: 4096,
    temperature: 0
}')

RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$URL" \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    -d "$BODY")

HTTP_CODE=$(echo "$RESPONSE" | tail -1)
BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_CODE" != "200" ]; then
    ERROR=$(echo "$BODY" | jq -r '.error.message // "HTTP error '"$HTTP_CODE"'"')
    echo "Error: $ERROR" >&2
    exit 1
fi

ANSWER=$(echo "$BODY" | jq -r '.choices[0].message.content // empty')

if [ -z "$ANSWER" ]; then
    echo "Error: No answer received" >&2
    exit 1
fi

echo "$ANSWER"
