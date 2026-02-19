#!/bin/bash
# Run this inside the bob container to test streaming behaviour:
#   docker compose exec bob bash /workspace/test_stream.sh
#
# It runs claude with --output-format stream-json on a trivial task and
# prints each output line with a wall-clock timestamp so we can see whether
# lines arrive incrementally or all at once at the very end.

set -e
echo "Testing: claude -p 'What is 2+2?' --output-format stream-json"
echo "Each line is prefixed with seconds since start."
echo "---"

START=$(python3 -c "import time; print(int(time.time()*1000))")
claude -p "What is 2+2?" \
  --output-format stream-json \
  --dangerously-skip-permissions \
  --verbose \
  2>&1 | \
while IFS= read -r line; do
  NOW=$(python3 -c "import time; print(int(time.time()*1000))")
  MS=$(( NOW - START ))
  printf "[%6d ms] %s\n" "$MS" "$line"
done

echo "---"
echo "Done."
