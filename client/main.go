package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "chat-application/gen/chat"
)

func main() {
	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	var currentRoom string

	client := pb.NewChatServiceClient(conn)

	// Get username
	fmt.Print("Enter your username: ")
	reader := bufio.NewReader(os.Stdin)
	username, _ := reader.ReadString('\n')
	username = strings.TrimSpace(username)

	// Join the general chat
	stream, err := client.ChatStream(context.Background())
	if err != nil {
		log.Fatalf("error opening chat stream: %v", err)
	}

	// Send initial message to register
	if err := stream.Send(&pb.ChatMessage{User: username, Content: "joined the chat"}); err != nil {
		log.Fatalf("error sending registration message: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine to send messages
	go func() {
		defer wg.Done()
		for {
			fmt.Print("> ")
			text, _ := reader.ReadString('\n')
			text = strings.TrimSpace(text)

			if strings.HasPrefix(text, "/private ") {
				parts := strings.SplitN(text, " ", 3)
				if len(parts) < 3 {
					fmt.Println("Usage: /private <user> <message>")
					continue
				}
				to := parts[1]
				content := parts[2]

				resp, err := client.SendPrivateMessage(context.Background(), &pb.PrivateMessageRequest{
					From:    username,
					To:      to,
					Content: content,
				})
				if err != nil {
					log.Printf("error sending private message: %v", err)
					continue
				}
				if !resp.Success {
					log.Printf("failed to send private message: %s", resp.Message)
				}
				continue
			}

			if strings.HasPrefix(text, "/join ") {
				if currentRoom != "" {
					fmt.Printf("You are already in room '%s'. Please exit first with /exit\n", currentRoom)
					continue
				}
				room := strings.TrimPrefix(text, "/join ")
				room = strings.TrimSpace(room)
				currentRoom = room

				roomStream, err := client.JoinChatRoom(context.Background(), &pb.ChatRoomRequest{
					User: username,
					Room: room,
				})
				if err != nil {
					log.Printf("error joining room: %v", err)
					continue
				}

				go func() {
					for {
						msg, err := roomStream.Recv()
						if err != nil {
							log.Printf("room stream error: %v", err)
							return
						}
						fmt.Printf("[%s] %s: %s\n", room, msg.User, msg.Content)
					}
				}()
				continue
			}

			if strings.HasPrefix(text, "/exit") {
				if currentRoom == "" {
					fmt.Println("Not currently in any room")
					continue
				}
			
				// Send special "exit" request
				exitReq := &pb.ChatRoomRequest{
					User: username,
					Room: "!exit", // Special room name to indicate exit
				}
			
				// We reuse the JoinChatRoom RPC but with special room name
				exitStream, err := client.JoinChatRoom(context.Background(), exitReq)
				if err != nil {
					log.Printf("error leaving room: %v", err)
					continue
				}
			
				// Wait for the exit confirmation
				msg, err := exitStream.Recv()
				if err != nil {
					log.Printf("error receiving exit confirmation: %v", err)
					continue
				}
			
				fmt.Println(msg.Content)
				currentRoom = ""
				continue
			}

			// Send regular chat message
			if currentRoom != "" {
				// Send room-specific message
				if err := stream.Send(&pb.ChatMessage{
					User:    username,
					Content: text,
					Room:    currentRoom,  // Add this line
				}); err != nil {
					log.Printf("error sending message: %v", err)
					return
				}
			} else {
				// Send general chat message
				if err := stream.Send(&pb.ChatMessage{
					User:    username,
					Content: text,
				}); err != nil {
					log.Printf("error sending message: %v", err)
					return
				}
			}
		}
	}()

	// Goroutine to receive messages
	go func() {
		defer wg.Done()
		for {
			msg, err := stream.Recv()
			if err != nil {
				log.Printf("error receiving message: %v", err)
				return
			}
			if msg.User != username { // Don't echo our own messages
				fmt.Printf("\n%s: %s\n> ", msg.User, msg.Content)
			}
		}
	}()

	wg.Wait()
}