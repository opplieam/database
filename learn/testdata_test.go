package learn

import "database/storage"

// testMovies is shared test data for integration tests.
var testMovies = []storage.MovieRecord{
	{1, "Toy Story", "Adventure|Animation"},
	{2, "Jumanji", "Adventure|Children"},
	{3, "Heat", "Action"},
	{4, "Sabrina", "Comedy|Romance"},
	{5, "Tom and Huck", "Adventure|Children"},
	{6, "Sudden Death", "Action"},
	{7, "GoldenEye", "Action|Adventure|Thriller"},
	{8, "American President", "Comedy|Drama|Romance"},
	{9, "Dracula", "Horror|Romance"},
	{10, "Balto", "Animation|Children"},
}
