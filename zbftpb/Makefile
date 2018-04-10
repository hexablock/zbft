

protoc:
	protoc zbft.proto -I ./ -I ../../../../ --gogofaster_out=plugins=grpc:../../../../
	@# TODO This needs a permanent fix
	sed -i -e '/import bcpb "bcpb"/d' zbft.pb.go && rm zbft.pb.go-e
