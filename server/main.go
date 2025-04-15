package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "chat-application/gen/chat"

)

type chatServer struct {
	pb.UnimplementedChatServiceServer

	mu           sync.RWMutex
	chatRooms    map[string]map[string]chan *pb.ChatRoomMessage
	chatStreams  map[string]chan *pb.ChatMessage
	privateUsers map[string]chan *pb.ChatMessage
}

func newChatServer() *chatServer {
	return &chatServer{
		chatRooms:    make(map[string]map[string]chan *pb.ChatRoomMessage),
		chatStreams:  make(map[string]chan *pb.ChatMessage),
		privateUsers: make(map[string]chan *pb.ChatMessage),
	}
}

// SendPrivateMessage implements unary RPC for private messages
func (s *chatServer) SendPrivateMessage(ctx context.Context, req *pb.PrivateMessageRequest) (*pb.PrivateMessageResponse, error) {
	s.mu.RLock()
	recipientChan, ok := s.privateUsers[req.To]
	s.mu.RUnlock()

	if !ok {
		return &pb.PrivateMessageResponse{
			Success: false,
			Message: "Recipient not found or not online",
		}, nil
	}

	// Send the private message
	recipientChan <- &pb.ChatMessage{
		User:    req.From,
		Content: req.Content,
	}

	return &pb.PrivateMessageResponse{
		Success: true,
		Message: "Private message sent successfully",
	}, nil
}

// JoinChatRoom implements server-side streaming for chatroom updates
func (s *chatServer) JoinChatRoom(req *pb.ChatRoomRequest, stream pb.ChatService_JoinChatRoomServer) error {
	// Create a channel for this user in the chat room
	userChan := make(chan *pb.ChatRoomMessage, 100)

	s.mu.Lock()
	if _, ok := s.chatRooms[req.Room]; !ok {
		s.chatRooms[req.Room] = make(map[string]chan *pb.ChatRoomMessage)
	}
	s.chatRooms[req.Room][req.User] = userChan
	s.mu.Unlock()

	// Notify others in the room
	s.mu.RLock()
	for _, ch := range s.chatRooms[req.Room] {
		if ch != userChan { // Don't send to ourselves
			ch <- &pb.ChatRoomMessage{
				User:    "System",
				Content: fmt.Sprintf("%s joined the room", req.User),
			}
		}
	}
	s.mu.RUnlock()

	// Cleanup when done
	defer func() {
		s.mu.Lock()
		delete(s.chatRooms[req.Room], req.User)
		if len(s.chatRooms[req.Room]) == 0 {
			delete(s.chatRooms, req.Room)
		}
		s.mu.Unlock()
	}()

	// Stream messages to the client
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case msg := <-userChan:
			if err := stream.Send(msg); err != nil {
				return err
			}
		}
	}
}

// ChatStream implements bidirectional streaming for continuous chat
func (s *chatServer) ChatStream(stream pb.ChatService_ChatStreamServer) error {
	// Register the user
	firstMsg, err := stream.Recv()
	if err != nil {
		return err
	}

	user := firstMsg.User
	if user == "" {
		return status.Error(codes.InvalidArgument, "username is required")
	}

	// Create a channel for this user's stream
	userChan := make(chan *pb.ChatMessage, 100)

	s.mu.Lock()
	s.chatStreams[user] = userChan
	s.privateUsers[user] = userChan // Also register for private messages
	s.mu.Unlock()

	// Cleanup when done
	defer func() {
		s.mu.Lock()
		delete(s.chatStreams, user)
		delete(s.privateUsers, user)
		s.mu.Unlock()
	}()

	// Goroutine to receive messages from client
	go func() {
		for {
			msg, err := stream.Recv()
        if err != nil {
            return
        }

        if msg.Room != "" {
            // Room-specific message
            s.mu.RLock()
            if roomUsers, exists := s.chatRooms[msg.Room]; exists {
                for username, ch := range roomUsers {
                    if username != msg.User { // Don't echo to sender
                        ch <- &pb.ChatRoomMessage{
                            User:    msg.User,
                            Content: msg.Content,
                        }
                    }
                }
            }
            s.mu.RUnlock()
        } else {
            // General message
            s.mu.RLock()
            for _, ch := range s.chatStreams {
                ch <- msg
            }
            s.mu.RUnlock()
        }
		}
	}()

	// Send messages to client
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case msg := <-userChan:
			if err := stream.Send(msg); err != nil {
				return err
			}
		}
	}
}

func main() {
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterChatServiceServer(grpcServer, newChatServer())

	log.Println("Server started on :50051")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}