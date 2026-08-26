# Validation targets useful during development and before performing actions

.PHONY: check-tools check-cluster check-ingress

check-tools:
	@bash src/engine/check.sh tools

check-cluster:
	@bash src/engine/check.sh cluster

check-ingress:
	@bash src/engine/check.sh ingress
