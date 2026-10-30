# Consumed by GoReleaser (dockers_v2): it places the prebuilt binary for each
# platform at $TARGETPLATFORM/dra-inspect in the build context.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG TARGETPLATFORM
LABEL org.opencontainers.image.title="dra-inspect" \
      org.opencontainers.image.source="https://github.com/johnahull/k8s-dra-inspect" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY ${TARGETPLATFORM}/dra-inspect /dra-inspect
USER 65532:65532
ENTRYPOINT ["/dra-inspect"]
