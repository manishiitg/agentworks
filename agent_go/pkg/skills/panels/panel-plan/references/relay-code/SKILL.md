# Relay code and publishing

Load relay-builder before editing relay.py or its configuration. Use relay action=test with the person’s supplied INPUT and relay action=get_run for real results. Use relay action=releases and relay action=publish only for requested publication. The graph is derived from the existing Python source; do not create a Goal plan or add Goal steps. A published API version and a dashboard are separate artifacts. Callers keep their selected immutable version while later Builder edits remain a draft.
