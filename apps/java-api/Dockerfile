# Multi-stage: compile with the JDK, ship on the JRE.
#
# No build tool by design. The app has no dependencies, so javac is the whole
# build — which keeps the lab's "activate offline in seconds" property that a
# Maven or Gradle dependency resolution would break.
FROM eclipse-temurin:21-jdk-alpine AS builder
WORKDIR /build
COPY src/ ./src/
RUN javac -d classes src/App.java

FROM eclipse-temurin:21-jre-alpine
ARG VERSION=dev
ENV APP_VERSION=${VERSION}

RUN addgroup -S appgroup && adduser -S appuser -G appgroup
COPY --from=builder /build/classes /app/classes
USER appuser
EXPOSE 8080

# Container-aware heap sizing. Without it the JVM sizes the heap from the NODE's
# memory, ignores the container limit, and is OOMKilled by the kernel long before
# it ever reports memory pressure of its own.
ENV JAVA_TOOL_OPTIONS="-XX:MaxRAMPercentage=75 -XX:+UseSerialGC -XX:TieredStopAtLevel=1"

ENTRYPOINT ["java", "-cp", "/app/classes", "App"]
