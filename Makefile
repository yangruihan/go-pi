.PHONY: build run test clean

BINARY_NAME=gopi
BUILD_DIR=./build

build:
	@echo "构建 gopi..."
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/gopi/
	@echo "构建完成: $(BUILD_DIR)/$(BINARY_NAME)"

run: build
	$(BUILD_DIR)/$(BINARY_NAME)

test:
	@echo "运行单元测试..."
	go test ./... -v -timeout 30s

test-short:
	go test ./... -short -timeout 10s

lint:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf $(BUILD_DIR)

.DEFAULT_GOAL := build
