You are working in a Git repository checked out in your current directory. Make the change
requested below in this repository.

## Request

{{REQUEST}}

## Tests you can see

{{VISIBLE_TESTS}}

## How to work

- Read the code you need before changing it.
- Make the smallest change that fully does what the request asks.
- Run the tests you can see, plus any existing tests near the code you change, and make sure
  they pass. You have no network access; use only what is already in the repository and the
  local module cache.
- Do not delete or weaken existing tests.
- If the request is ambiguous, contradictory, or impossible, do not guess. Stop and ask: reply
  with status "needs_clarification" and put your question in "question".

## Final answer

When you are finished, reply with only a JSON object, and nothing else:

{"status": "done" | "needs_clarification", "summary": "<what you changed, or why you stopped>", "question": "<only when needs_clarification>"}
