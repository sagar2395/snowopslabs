# Runtime management — dispatches to the correct runtime based on PROFILE
# Supported profiles: k3d (local), kind (headless/CI), incluster (shared server)

runtime-up:
	@echo "Creating $(PROFILE) cluster '$(CLUSTER_NAME)'..."
	@bash runtimes/$(PROFILE)/up.sh $(CLUSTER_NAME)

runtime-down:
	@echo "Shutting down $(PROFILE) cluster..."
	@bash runtimes/$(PROFILE)/down.sh $(CLUSTER_NAME)

runtime-status:
	@echo "Runtime: $(PROFILE)"
	@echo "Cluster: $(CLUSTER_NAME)"
	@echo ""
	@kubectl cluster-info 2>/dev/null || echo "Cluster not reachable"
	@echo ""
	@kubectl get nodes -o wide 2>/dev/null || true
