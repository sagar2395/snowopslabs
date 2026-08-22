# Provider variables — override in .env or on the command line.
# These must match a subdirectory under platform/<category>/<provider>/.
INGRESS_PROVIDER  ?= traefik
METRICS_PROVIDER  ?= prometheus
# LOGGING_PROVIDER ?= loki          # set to enable logging stack
# TRACING_PROVIDER ?= tempo         # set to enable tracing stack
# MESH_PROVIDER    ?= istio         # set to enable service mesh (istio|linkerd)
# AUTOSCALING_PROVIDER ?= keda      # set to enable autoscaling (keda)

platform-up: platform-ingress-up platform-monitoring-up
ifdef LOGGING_PROVIDER
platform-up: platform-logging-up
endif
ifdef TRACING_PROVIDER
platform-up: platform-tracing-up
endif
ifdef MESH_PROVIDER
platform-up: platform-mesh-up
endif
ifdef AUTOSCALING_PROVIDER
platform-up: platform-autoscaling-up
endif

platform-down: platform-monitoring-down platform-ingress-down
ifdef LOGGING_PROVIDER
platform-down: platform-logging-down
endif
ifdef TRACING_PROVIDER
platform-down: platform-tracing-down
endif
ifdef MESH_PROVIDER
platform-down: platform-mesh-down
endif
ifdef AUTOSCALING_PROVIDER
platform-down: platform-autoscaling-down
endif

platform-status: platform-ingress-status platform-monitoring-status
ifdef LOGGING_PROVIDER
platform-status: platform-logging-status
endif
ifdef TRACING_PROVIDER
platform-status: platform-tracing-status
endif
ifdef MESH_PROVIDER
platform-status: platform-mesh-status
endif
ifdef AUTOSCALING_PROVIDER
platform-status: platform-autoscaling-status
endif

# Ingress (INGRESS_PROVIDER=traefik|nginx)
platform-ingress-up:
	@echo "[platform] ingress provider: $(INGRESS_PROVIDER)"
	bash platform/ingress/$(INGRESS_PROVIDER)/install.sh

platform-ingress-down:
	@echo "[platform] removing ingress provider: $(INGRESS_PROVIDER)"
	@bash platform/ingress/$(INGRESS_PROVIDER)/uninstall.sh

platform-ingress-status:
	@echo "=== Ingress ($(INGRESS_PROVIDER)) Status ==="
	@bash platform/ingress/$(INGRESS_PROVIDER)/status.sh

# Monitoring: metrics backend + Grafana (always paired)
platform-monitoring-up:
	@echo "[platform] metrics provider: $(METRICS_PROVIDER)"
	bash platform/monitoring/metrics/$(METRICS_PROVIDER)/install.sh
	bash platform/monitoring/grafana/install.sh

platform-monitoring-down:
	@echo "[platform] removing monitoring stack ($(METRICS_PROVIDER) + grafana)"
	@bash platform/monitoring/grafana/uninstall.sh
	@bash platform/monitoring/metrics/$(METRICS_PROVIDER)/uninstall.sh

platform-monitoring-status:
	@echo "=== Metrics ($(METRICS_PROVIDER)) Status ==="
	@bash platform/monitoring/metrics/$(METRICS_PROVIDER)/status.sh
	@echo ""
	@echo "=== Grafana Status ==="
	@bash platform/monitoring/grafana/status.sh

# Logging (LOGGING_PROVIDER=loki|...) — only when LOGGING_PROVIDER is set
platform-logging-up:
	@echo "[platform] logging provider: $(LOGGING_PROVIDER)"
	bash platform/logging/$(LOGGING_PROVIDER)/install.sh

platform-logging-down:
	@echo "[platform] removing logging provider: $(LOGGING_PROVIDER)"
	@bash platform/logging/$(LOGGING_PROVIDER)/uninstall.sh

platform-logging-status:
	@echo "=== Logging ($(LOGGING_PROVIDER)) Status ==="
	@bash platform/logging/$(LOGGING_PROVIDER)/status.sh

# Tracing (TRACING_PROVIDER=tempo|...) — only when TRACING_PROVIDER is set
platform-tracing-up:
	@echo "[platform] tracing provider: $(TRACING_PROVIDER)"
	bash platform/tracing/$(TRACING_PROVIDER)/install.sh

platform-tracing-down:
	@echo "[platform] removing tracing provider: $(TRACING_PROVIDER)"
	@bash platform/tracing/$(TRACING_PROVIDER)/uninstall.sh

platform-tracing-status:
	@echo "=== Tracing ($(TRACING_PROVIDER)) Status ==="
	@bash platform/tracing/$(TRACING_PROVIDER)/status.sh

# Service mesh (MESH_PROVIDER=istio|linkerd) — only when MESH_PROVIDER is set
platform-mesh-up:
	@echo "[platform] mesh provider: $(MESH_PROVIDER)"
	bash platform/mesh/$(MESH_PROVIDER)/install.sh

platform-mesh-down:
	@echo "[platform] removing mesh provider: $(MESH_PROVIDER)"
	@bash platform/mesh/$(MESH_PROVIDER)/uninstall.sh

platform-mesh-status:
	@echo "=== Mesh ($(MESH_PROVIDER)) Status ==="
	@bash platform/mesh/$(MESH_PROVIDER)/status.sh

# Data infrastructure (additive sub-components: kafka, postgres).
# Address each independently — they coexist like the monitoring/* components.
platform-data-kafka-up:
	@echo "[platform] data: kafka (Strimzi)"
	bash platform/data/kafka/install.sh

platform-data-kafka-down:
	@echo "[platform] removing data: kafka"
	@bash platform/data/kafka/uninstall.sh

platform-data-kafka-status:
	@echo "=== Data: Kafka Status ==="
	@bash platform/data/kafka/status.sh

platform-data-postgres-up:
	@echo "[platform] data: postgres (CloudNativePG)"
	bash platform/data/postgres/install.sh

platform-data-postgres-down:
	@echo "[platform] removing data: postgres"
	@bash platform/data/postgres/uninstall.sh

platform-data-postgres-status:
	@echo "=== Data: Postgres Status ==="
	@bash platform/data/postgres/status.sh

# Convenience aggregates for the whole data category.
platform-data-up: platform-data-kafka-up platform-data-postgres-up
platform-data-down: platform-data-postgres-down platform-data-kafka-down
platform-data-status: platform-data-kafka-status platform-data-postgres-status

# Autoscaling (AUTOSCALING_PROVIDER=keda) — only when AUTOSCALING_PROVIDER is set
platform-autoscaling-up:
	@echo "[platform] autoscaling provider: $(AUTOSCALING_PROVIDER)"
	bash platform/autoscaling/$(AUTOSCALING_PROVIDER)/install.sh

platform-autoscaling-down:
	@echo "[platform] removing autoscaling provider: $(AUTOSCALING_PROVIDER)"
	@bash platform/autoscaling/$(AUTOSCALING_PROVIDER)/uninstall.sh

platform-autoscaling-status:
	@echo "=== Autoscaling ($(AUTOSCALING_PROVIDER)) Status ==="
	@bash platform/autoscaling/$(AUTOSCALING_PROVIDER)/status.sh

# Secrets management (vault backend + external-secrets sync operator).
# ESO depends on Vault — install vault first.
platform-secrets-vault-up:
	@echo "[platform] secrets: vault (dev mode)"
	bash platform/secrets/vault/install.sh

platform-secrets-vault-down:
	@echo "[platform] removing secrets: vault"
	@bash platform/secrets/vault/uninstall.sh

platform-secrets-vault-status:
	@echo "=== Secrets: Vault Status ==="
	@bash platform/secrets/vault/status.sh

platform-secrets-eso-up:
	@echo "[platform] secrets: external-secrets"
	bash platform/secrets/external-secrets/install.sh

platform-secrets-eso-down:
	@echo "[platform] removing secrets: external-secrets"
	@bash platform/secrets/external-secrets/uninstall.sh

platform-secrets-eso-status:
	@echo "=== Secrets: External Secrets Status ==="
	@bash platform/secrets/external-secrets/status.sh

# Aggregates: bring up vault then ESO (order matters); tear down in reverse.
platform-secrets-up: platform-secrets-vault-up platform-secrets-eso-up
platform-secrets-down: platform-secrets-eso-down platform-secrets-vault-down
platform-secrets-status: platform-secrets-vault-status platform-secrets-eso-status

.PHONY: platform-up platform-down platform-status \
        platform-ingress-up platform-ingress-down platform-ingress-status \
        platform-monitoring-up platform-monitoring-down platform-monitoring-status \
        platform-logging-up platform-logging-down platform-logging-status \
        platform-tracing-up platform-tracing-down platform-tracing-status \
        platform-mesh-up platform-mesh-down platform-mesh-status \
        platform-autoscaling-up platform-autoscaling-down platform-autoscaling-status \
        platform-data-up platform-data-down platform-data-status \
        platform-data-kafka-up platform-data-kafka-down platform-data-kafka-status \
        platform-data-postgres-up platform-data-postgres-down platform-data-postgres-status \
        platform-secrets-up platform-secrets-down platform-secrets-status \
        platform-secrets-vault-up platform-secrets-vault-down platform-secrets-vault-status \
        platform-secrets-eso-up platform-secrets-eso-down platform-secrets-eso-status
