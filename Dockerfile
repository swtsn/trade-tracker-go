FROM gcr.io/distroless/static-debian12
COPY bin/trade-tracker-linux /trade-tracker
ENTRYPOINT ["/trade-tracker"]
