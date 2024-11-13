FROM golang:1.22.4-bookworm AS build-env

# Installer les dépendances pour la construction
RUN apt-get update && apt-get install -y build-essential

# Configurer le répertoire de travail pour la construction
WORKDIR /go/src/github.com/Helios-Chain-Labs/ethermint

# Copier les fichiers sources
COPY . .

# Construire le binaire
RUN make build

# Image finale
FROM debian:bookworm-slim AS stage-1

# Installer les certificats et jq
RUN apt-get update && apt-get install -y ca-certificates jq

# Configurer le répertoire de travail
WORKDIR /

# Copier le binaire depuis l'étape de construction
COPY --from=build-env /go/src/github.com/Helios-Chain-Labs/ethermint/build/ethermintd /usr/bin/ethermintd

# Exécuter ethermintd par défaut
CMD ["ethermintd"]
