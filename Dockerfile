FROM gcr.io/distroless/static-debian12
COPY trade-tracker-linux /trade-tracker
ENTRYPOINT ["/trade-tracker"]
