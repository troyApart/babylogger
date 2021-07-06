FROM golang:1.15.11-alpine3.13 as build
RUN apk add ca-certificates
ADD . /babylogger
RUN mkdir -p /babylogger/build
RUN /babylogger/bin/build

FROM alpine:3.13
RUN apk add ca-certificates tzdata
WORKDIR /
COPY --from=build /babylogger/build/babylogger /var/task/lambda

# (Optional) Add Lambda Runtime Interface Emulator and use a script in the ENTRYPOINT for simpler local runs
# Swap out the commented code here with the code below
# ADD https://github.com/aws/aws-lambda-runtime-interface-emulator/releases/latest/download/aws-lambda-rie /usr/bin/aws-lambda-rie
# RUN chmod 755 /usr/bin/aws-lambda-rie
# COPY bin/entry.sh /
# RUN chmod 755 /entry.sh
# ENTRYPOINT [ "/entry.sh" ]
# CMD [ "/var/task/lambda" ]
ENTRYPOINT [ "/var/task/lambda" ]
