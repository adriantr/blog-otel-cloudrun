FROM golang:alpine

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY src/*.go ./

RUN go build -o /uuidgenerator

EXPOSE 8080

CMD [ "/uuidgenerator" ]
