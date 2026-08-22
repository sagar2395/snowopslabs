# Validation targets useful during development and before performing actions

.PHONY: check-tools check-cluster check-ingress

check-tools:
	@bash engine/check.sh tools

check-cluster:
	@bash engine/check.sh cluster

check-ingress:
	@bash engine/check.sh ingress
