package learn

import "database/storage"

// testMovies is shared test data for integration tests.
var testMovies = []storage.MovieRecord{
	{MovieId: 1, Title: "Toy Story", Genres: "Adventure|Animation"},
	{MovieId: 2, Title: "Jumanji", Genres: "Adventure|Children"},
	{MovieId: 3, Title: "Heat", Genres: "Action"},
	{MovieId: 4, Title: "Sabrina", Genres: "Comedy|Romance"},
	{MovieId: 5, Title: "Tom and Huck", Genres: "Adventure|Children"},
	{MovieId: 6, Title: "Sudden Death", Genres: "Action"},
	{MovieId: 7, Title: "GoldenEye", Genres: "Action|Adventure|Thriller"},
	{MovieId: 8, Title: "American President", Genres: "Comedy|Drama|Romance"},
	{MovieId: 9, Title: "Dracula", Genres: "Horror|Romance"},
	{MovieId: 10, Title: "Balto", Genres: "Animation|Children"},
}
