package board

// Store defines the persistence interface for the board system.
type Store interface {
	// Board CRUD
	CreateBoard(b *Board) error
	GetBoard(id string) (*Board, error)
	ListBoards() ([]*Board, error)
	UpdateBoard(b *Board) error
	DeleteBoard(id string) error

	// Card CRUD
	CreateCard(c *Card) error
	GetCard(id string) (*Card, error)
	ListCards(boardID string) ([]*Card, error)
	ListCardsByColumn(boardID, columnID string) ([]*Card, error)
	ListCardsByAssignee(agentID string) ([]*Card, error)
	UpdateCard(c *Card) error
	MoveCard(cardID, toColumn string, position int) error
	DeleteCard(id string) error

	// Graph links
	AddLink(l *Link) error
	RemoveLink(id string) error
	GetLinks(cardID string) ([]*Link, error)

	// Agent CRUD
	CreateAgent(a *Agent) error
	GetAgent(id string) (*Agent, error)
	ListAgents() ([]*Agent, error)
	UpdateAgent(a *Agent) error
	DeleteAgent(id string) error
}
