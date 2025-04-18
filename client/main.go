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

func handleCommand(text, username string, currentRoom string, client pb.ChatServiceClient, currentRoomPtr *string) {
	switch {
	case strings.HasPrefix(text, "/private "):
		handlePrivateMessage(text, username, client)
	case strings.HasPrefix(text, "/join "):
		handleJoinRoom(text, username, client, currentRoomPtr)
	case strings.HasPrefix(text, "/exit"):
		handleExitRoom(username, client, currentRoomPtr)
	default:
		fmt.Println("Unknown command")
	}
}

func handlePrivateMessage(text, username string, client pb.ChatServiceClient) {
	parts := strings.SplitN(text, " ", 3)
	if len(parts) < 3 {
		fmt.Println("Usage: /private <user> <message>")
		return
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
		return
	}
	if !resp.Success {
		log.Printf("failed to send private message: %s", resp.Message)
	}
}

func handleJoinRoom(text, username string, client pb.ChatServiceClient, currentRoomPtr *string) {
	if *currentRoomPtr != "" {
		fmt.Printf("You are already in room '%s'. Please exit first with /exit\n", *currentRoomPtr)
		return
	}

	room := strings.TrimPrefix(text, "/join ")
	room = strings.TrimSpace(room)
	*currentRoomPtr = room

	roomStream, err := client.JoinChatRoom(context.Background(), &pb.ChatRoomRequest{
		User: username,
		Room: room,
	})
	if err != nil {
		log.Printf("error joining room: %v", err)
		return
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
}

func handleExitRoom(username string, client pb.ChatServiceClient, currentRoomPtr *string) {
	if *currentRoomPtr == "" {
		fmt.Println("Not currently in any room")
		return
	}

	exitReq := &pb.ChatRoomRequest{
		User: username,
		Room: "!exit",
	}

	exitStream, err := client.JoinChatRoom(context.Background(), exitReq)
	if err != nil {
		log.Printf("error leaving room: %v", err)
		return
	}

	msg, err := exitStream.Recv()
	if err != nil {
		log.Printf("error receiving exit confirmation: %v", err)
		return
	}

	fmt.Println(msg.Content)
	*currentRoomPtr = ""
}

func sendChatMessage(text, username, currentRoom string, stream pb.ChatService_ChatStreamClient) {
	var msg *pb.ChatMessage
	if currentRoom != "" {
		msg = &pb.ChatMessage{
			User:    username,
			Content: text,
			Room:    currentRoom,
		}
	} else {
		msg = &pb.ChatMessage{
			User:    username,
			Content: text,
		}
	}

	if err := stream.Send(msg); err != nil {
		log.Printf("error sending message: %v", err)
	}
}

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

			if strings.HasPrefix(text, "/") {
				handleCommand(text, username, currentRoom, client, &currentRoom)
				continue
			}

			sendChatMessage(text, username, currentRoom, stream)
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
				if msg.Room != "" && msg.Room != currentRoom {
					// Skip messages from other rooms
					continue
				}
				roomPrefix := ""
				if msg.Room != "" {
					roomPrefix = fmt.Sprintf("[%s] ", msg.Room)
				}
				fmt.Printf("\n%s%s: %s\n> ", roomPrefix, msg.User, msg.Content)
			}
		}
	}()

	wg.Wait()
}