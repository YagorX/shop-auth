package main

// import (
// 	"log"

// 	"google.golang.org/grpc"
// 	"google.golang.org/grpc/credentials/insecure"

// 	// Импорт сгенерированных protobuf файлов
// 	ssov1 "github.com/YagorX/protos/gen/go/sso"
// )

// func main() {
// 	conn, err := grpc.NewClient("localhost:44044",
// 		grpc.WithTransportCredentials(insecure.NewCredentials()),
// 	)

// 	if err != nil {
// 		log.Fatalf("failed to connect: %v", err)
// 	}
// 	defer conn.Close()

// 	// Создание клиента
// 	client := ssov1.NewAuthClient(conn)

// 	// Вызов метода IsAdmin
// 	response, err := callIsAdmin(client, 12)
// 	if err != nil {
// 		log.Fatalf("failed to call IsAdmin: %v", err)
// 	}

// 	log.Printf("IsAdmin response: %v", response.IsAdmin)
// }

// func callIsAdmin(client ssov1.AuthClient, userID int64) (*ssov1.IsAdminResponse, error) {
// 	// Создание контекста с таймаутом
// 	// ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	// defer cancel()

// 	// Создание запроса
// 	// req := &ssov1.IsAdminRequest{
// 	// 	UserId: userID,
// 	// }
// 	// return client.IsAdmin(ctx, req)
// }
