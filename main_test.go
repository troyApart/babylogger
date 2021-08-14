package main

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/expression"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type fakeDynamodb struct {
	mock.Mock
}

func (f *fakeDynamodb) PutItem(input *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
	ret := f.Called(input)
	return ret.Get(0).(*dynamodb.PutItemOutput), ret.Error(1)
}
func (f *fakeDynamodb) UpdateItem(input *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
	ret := f.Called(input)
	return ret.Get(0).(*dynamodb.UpdateItemOutput), ret.Error(1)
}
func (f *fakeDynamodb) Query(input *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
	ret := f.Called(input)
	return ret.Get(0).(*dynamodb.QueryOutput), ret.Error(1)
}

func TestNextFeed(t *testing.T) {
	outputDate := "2021-06-19"
	outputTime := "18:32"
	outputTimestampParse, err := time.Parse("2006-01-02 15:04", fmt.Sprintf("%s %s", outputDate, outputTime))
	assert.Nil(t, err)
	outputTimestamp := outputTimestampParse.Unix()
	outputSide := "left"
	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil).Once()
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{
		{"timestamp": &dynamodb.AttributeValue{N: aws.String(strconv.FormatInt(outputTimestamp, 10))}, "side": &dynamodb.AttributeValue{S: aws.String(outputSide)}},
	}}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding", FeedingInterval: 3 * time.Hour},
		dynamodb: fdb,
	}

	message := "next"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	previousTime, err := time.Parse("2006-01-02 15:04", fmt.Sprintf("%s %s", outputDate, outputTime))
	assert.Nil(t, err)
	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("The next feeding is on your %s side on %s", RightSide, previousTime.Add(3*time.Hour).In(loc).Format("Jan 2 03:04PM"))
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestNextFeed_Bottle(t *testing.T) {
	outputDate := "2021-06-19"
	outputTime := "18:32"
	outputTimeIgnore := "14:21"
	outputSideSkip := "bottle"
	outputTimestampParse, err := time.Parse("2006-01-02 15:04", fmt.Sprintf("%s %s", outputDate, outputTime))
	assert.Nil(t, err)
	outputTimestamp := outputTimestampParse.Unix()
	outputTimestampIgnoreParse, err := time.Parse("2006-01-02 15:04", fmt.Sprintf("%s %s", outputDate, outputTimeIgnore))
	assert.Nil(t, err)
	outputTimestampIgnore := outputTimestampIgnoreParse.Unix()
	outputSide := "left"
	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil).Once()
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{
		{"timestamp": &dynamodb.AttributeValue{N: aws.String(strconv.FormatInt(outputTimestamp, 10))}, "side": &dynamodb.AttributeValue{S: aws.String(outputSideSkip)}},
	}}, nil).Once()
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{
		{"timestamp": &dynamodb.AttributeValue{N: aws.String(strconv.FormatInt(outputTimestampIgnore, 10))}, "side": &dynamodb.AttributeValue{S: aws.String(outputSide)}},
	}}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding", FeedingInterval: 3 * time.Hour},
		dynamodb: fdb,
	}

	message := "next 4h"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	previousTime, err := time.Parse("2006-01-02T15:04:05", fmt.Sprintf("%sT%s:00", outputDate, outputTime))
	assert.Nil(t, err)
	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("The next feeding is on your %s side on %s", RightSide, previousTime.Add(4*time.Hour).In(loc).Format("Jan 2 03:04PM"))
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestNextFeed_IntervalAndCount(t *testing.T) {
	outputDate := "2021-06-19"
	outputTime := "18:32"
	outputTimestampParse, err := time.Parse("2006-01-02 15:04", fmt.Sprintf("%s %s", outputDate, outputTime))
	assert.Nil(t, err)
	outputTimestamp := outputTimestampParse.Unix()
	outputSide := "left"
	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil).Once()
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{
		{"timestamp": &dynamodb.AttributeValue{N: aws.String(strconv.FormatInt(outputTimestamp, 10))}, "side": &dynamodb.AttributeValue{S: aws.String(outputSide)}},
	}}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding", FeedingInterval: 3 * time.Hour},
		dynamodb: fdb,
	}

	message := "next 4h 5"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	xmlResp := &Response{}
	xmlResp.Message = "The next feedings are:\nright: Jun 19 03:32PM\nleft: Jun 19 07:32PM\nright: Jun 19 11:32PM\nleft: Jun 20 03:32AM\nright: Jun 20 07:32AM"
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestNewFeed(t *testing.T) {
	current := time.Now().UTC()
	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil).Once()
	fdb.On("PutItem", &dynamodb.PutItemInput{TableName: aws.String("feeding"), Item: map[string]*dynamodb.AttributeValue{
		"userid": {
			N: aws.String("1"),
		},
		"timestamp": {
			N: aws.String(strconv.FormatInt(current.Unix(), 10)),
		},
		"side": {
			S: aws.String("left"),
		},
	}}).Return(&dynamodb.PutItemOutput{}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding"},
		dynamodb: fdb,
	}

	message := "feed left"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("New feeding recorded on %s starting on %s side", current.In(loc).Format("Jan 2 03:04PM"), "left")
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestNewFeed_DateAndTime(t *testing.T) {
	d := "2010-01-01"
	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	tiTime, err := time.ParseInLocation("2006-01-02 03:04 PM", fmt.Sprintf("%s 01:05 AM", d), loc)
	assert.Nil(t, err)
	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil).Once()
	fdb.On("PutItem", &dynamodb.PutItemInput{TableName: aws.String("feeding"), Item: map[string]*dynamodb.AttributeValue{
		"userid": {
			N: aws.String("1"),
		},
		"timestamp": {
			N: aws.String(strconv.FormatInt(tiTime.UTC().Unix(), 10)),
		},
		"side": {
			S: aws.String("right"),
		},
	}}).Return(&dynamodb.PutItemOutput{}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding"},
		dynamodb: fdb,
	}

	message := "feed right 2010-01-01 1:05 am"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	datetime, err := time.Parse("2006-01-02T15:04:05", fmt.Sprintf("%sT%s:00", d, tiTime.Format("15:04")))
	assert.Nil(t, err)

	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("New feeding recorded on %s starting on %s side", datetime.Format("Jan 2 03:04PM"), "right")
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestNewFeed_DateAndTime_24H(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	tiTime, err := time.ParseInLocation("2006-01-02 15:04", "2010-01-01 20:05", loc)
	assert.Nil(t, err)
	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil).Once()
	fdb.On("PutItem", &dynamodb.PutItemInput{TableName: aws.String("feeding"), Item: map[string]*dynamodb.AttributeValue{
		"userid": {
			N: aws.String("1"),
		},
		"timestamp": {
			N: aws.String(strconv.FormatInt(tiTime.UTC().Unix(), 10)),
		},
		"side": {
			S: aws.String("left"),
		},
	}}).Return(&dynamodb.PutItemOutput{}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding"},
		dynamodb: fdb,
	}

	message := "feed left 2010-01-01 20:05"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	datetime, err := time.Parse("2006-01-02 15:04", fmt.Sprintf("2010-01-01 %s", tiTime.Format("15:04")))
	assert.Nil(t, err)

	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("New feeding recorded on %s starting on %s side", datetime.Format("Jan 2 15:04"), "left")
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestNewFeed_DateAndTime_NoYear(t *testing.T) {
	d := "2021-07-01"
	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	tiTime, err := time.ParseInLocation("2006-01-02 03:04 PM", fmt.Sprintf("%s 01:05 AM", d), loc)
	assert.Nil(t, err)
	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil).Once()
	fdb.On("PutItem", &dynamodb.PutItemInput{TableName: aws.String("feeding"), Item: map[string]*dynamodb.AttributeValue{
		"userid": {
			N: aws.String("1"),
		},
		"timestamp": {
			N: aws.String(strconv.FormatInt(tiTime.UTC().Unix(), 10)),
		},
		"side": {
			S: aws.String("right"),
		},
	}}).Return(&dynamodb.PutItemOutput{}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding"},
		dynamodb: fdb,
	}

	message := "feed right 7/1 1:05 am"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	datetime, err := time.Parse("2006-01-02 15:04:05", fmt.Sprintf("%s %s:00", d, tiTime.Format("15:04")))
	assert.Nil(t, err)

	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("New feeding recorded on %s starting on %s side", datetime.Format("Jan 2 03:04PM"), "right")
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestNewFeed_DateAndTime_Right(t *testing.T) {
	d := "2010-01-01"
	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	tiTime, err := time.ParseInLocation("2006-01-02 03:04 PM", fmt.Sprintf("%s 01:05 AM", d), loc)
	assert.Nil(t, err)
	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil).Once()
	fdb.On("PutItem", &dynamodb.PutItemInput{TableName: aws.String("feeding"), Item: map[string]*dynamodb.AttributeValue{
		"userid": {
			N: aws.String("1"),
		},
		"timestamp": {
			N: aws.String(strconv.FormatInt(tiTime.UTC().Unix(), 10)),
		},
		"side": {
			S: aws.String("right"),
		},
		"right": {
			N: aws.String("15"),
		},
	}}).Return(&dynamodb.PutItemOutput{}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding"},
		dynamodb: fdb,
	}

	message := "feed right 15 2010-01-01 1:05 am"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	datetime, err := time.Parse("2006-01-02T15:04:05", fmt.Sprintf("%sT%s:00", d, tiTime.Format("15:04")))
	assert.Nil(t, err)

	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("New feeding recorded on %s starting on %s side", datetime.Format("Jan 2 03:04PM"), "right")
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestNewFeed_LeftAndRight(t *testing.T) {
	current := time.Now().UTC()
	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil)
	fdb.On("PutItem", &dynamodb.PutItemInput{TableName: aws.String("feeding"), Item: map[string]*dynamodb.AttributeValue{
		"userid": {
			N: aws.String("1"),
		},
		"timestamp": {
			N: aws.String(strconv.FormatInt(current.Unix(), 10)),
		},
		"side": {
			S: aws.String("left"),
		},
		"left": {
			N: aws.String("15"),
		},
		"right": {
			N: aws.String("10"),
		},
	}}).Return(&dynamodb.PutItemOutput{}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding"},
		dynamodb: fdb,
	}

	message := "feed left 15 right 10"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("New feeding recorded on %s starting on %s side", current.In(loc).Format("Jan 2 03:04PM"), "left")
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestUpdateFeed_Last(t *testing.T) {
	expectedDate := "2021-06-19"
	expectedTime := "18:32"
	expectedSide := "left"
	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	timeParse, err := time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %s", expectedDate, expectedTime), loc)
	assert.Nil(t, err)
	outputTimestamp := timeParse.UTC().Unix()

	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil).Once()
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{
		{"timestamp": &dynamodb.AttributeValue{N: aws.String(strconv.FormatInt(outputTimestamp, 10))}, "side": &dynamodb.AttributeValue{S: aws.String(expectedSide)}},
	}}, nil).Once()
	fdb.On("UpdateItem", mock.Anything).Return(&dynamodb.UpdateItemOutput{}, nil)
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding", FeedingInterval: 3 * time.Hour},
		dynamodb: fdb,
	}

	message := "update last left 10 right add 5"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("Updated feeding recorded on %s", timeParse.Format("Jan 2 03:04PM"))
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestUpdateFeed_LastBottle(t *testing.T) {
	expectedDate := "2021-06-19"
	expectedTime := "18:32"
	expectedSide := "bottle"
	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	timeParse, err := time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %s", expectedDate, expectedTime), loc)
	assert.Nil(t, err)
	outputTimestamp := timeParse.UTC().Unix()

	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil).Once()
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{
		{"timestamp": &dynamodb.AttributeValue{N: aws.String(strconv.FormatInt(outputTimestamp, 10))}, "side": &dynamodb.AttributeValue{S: aws.String(expectedSide)}},
	}}, nil).Once()
	fdb.On("UpdateItem", mock.Anything).Return(&dynamodb.UpdateItemOutput{}, nil)
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding", FeedingInterval: 3 * time.Hour},
		dynamodb: fdb,
	}

	message := "update last bottle add .5"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("Updated feeding recorded on %s", timeParse.Format("Jan 2 03:04PM"))
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestDiaper(t *testing.T) {
	current := time.Now().UTC()
	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil)
	fdb.On("PutItem", &dynamodb.PutItemInput{TableName: aws.String("diaper"), Item: map[string]*dynamodb.AttributeValue{
		"userid": {
			N: aws.String("1"),
		},
		"timestamp": {
			N: aws.String(strconv.FormatInt(current.Unix(), 10)),
		},
		"wet": {
			BOOL: aws.Bool(true),
		},
		"soiled": {
			BOOL: aws.Bool(true),
		},
		"checked": {
			S: aws.String("pre-feed"),
		},
	}}).Return(&dynamodb.PutItemOutput{}, nil).Once()
	bl := BabyLogger{
		config:   &Config{DiaperTableName: "diaper"},
		dynamodb: fdb,
	}

	message := "diaper wet soiled checked pre-feed"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("New diaper recorded on %s", current.In(loc).Format("Jan 2 03:04PM"))
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestListFeeds(t *testing.T) {
	expectedDate := "2021-06-19"
	expectedTime := "13:15"
	expectedTime2 := "20:30"

	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	timeParse, err := time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %s", expectedDate, expectedTime), loc)
	assert.Nil(t, err)
	outputTimestamp := timeParse.UTC().Unix()
	timeParse2, err := time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %s", expectedDate, expectedTime2), loc)
	assert.Nil(t, err)
	outputTimestamp2 := timeParse2.UTC().Unix()

	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil).Once()
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{
		{
			"timestamp": &dynamodb.AttributeValue{N: aws.String(strconv.FormatInt(outputTimestamp, 10))},
			"side":      &dynamodb.AttributeValue{S: aws.String("right")},
			"right":     &dynamodb.AttributeValue{N: aws.String("15")},
		},
		{
			"timestamp": &dynamodb.AttributeValue{N: aws.String(strconv.FormatInt(outputTimestamp2, 10))},
			"side":      &dynamodb.AttributeValue{S: aws.String("left")},
			"left":      &dynamodb.AttributeValue{N: aws.String("10")},
		},
	}}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding", FeedingInterval: 3 * time.Hour},
		dynamodb: fdb,
	}

	message := "list feeds 2021-06-19"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	xmlResp := &Response{}
	xmlResp.Message = "Feedings on 2021-06-19\n13:15 - right L: 0min R: 15min\n20:30 - left L: 10min R: 0min\nTotal: 2, Left: 10min, Right: 15min, Bottle: 0.00oz"
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestRouter_MultipleCommands(t *testing.T) {
	outputDate := "2021-06-19"
	outputTime := "18:32"
	outputTimestampParse, err := time.Parse("2006-01-02 15:04", fmt.Sprintf("%s %s", outputDate, outputTime))
	assert.Nil(t, err)
	outputTimestamp := outputTimestampParse.Unix()
	outputSide := "left"
	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil).Once()
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{
		{"timestamp": &dynamodb.AttributeValue{N: aws.String(strconv.FormatInt(outputTimestamp, 10))}, "side": &dynamodb.AttributeValue{S: aws.String(outputSide)}},
	}}, nil).Once()

	expectedDate := "2021-06-19"
	expectedTime := "13:15"
	expectedTime2 := "20:30"

	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	timeParse, err := time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %s", expectedDate, expectedTime), loc)
	assert.Nil(t, err)
	listOutputTimestamp := timeParse.UTC().Unix()
	timeParse2, err := time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %s", expectedDate, expectedTime2), loc)
	assert.Nil(t, err)
	listOutputTimestamp2 := timeParse2.UTC().Unix()

	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{
		{
			"timestamp": &dynamodb.AttributeValue{N: aws.String(strconv.FormatInt(listOutputTimestamp, 10))},
			"side":      &dynamodb.AttributeValue{S: aws.String("right")},
			"right":     &dynamodb.AttributeValue{N: aws.String("15")},
		},
		{
			"timestamp": &dynamodb.AttributeValue{N: aws.String(strconv.FormatInt(listOutputTimestamp2, 10))},
			"side":      &dynamodb.AttributeValue{S: aws.String("left")},
			"left":      &dynamodb.AttributeValue{N: aws.String("10")},
		},
	}}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding", FeedingInterval: 3 * time.Hour},
		dynamodb: fdb,
	}

	message := "next\nlist feeds 2021-06-19"
	resp, err := bl.Router(events.APIGatewayProxyRequest{HTTPMethod: http.MethodGet, QueryStringParameters: map[string]string{"Body": message}})
	assert.Nil(t, err)

	previousTime, err := time.Parse("2006-01-02 15:04", fmt.Sprintf("%s %s", outputDate, outputTime))
	assert.Nil(t, err)
	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("The next feeding is on your %s side on %s\nFeedings on 2021-06-19\n13:15 - right L: 0min R: 15min\n20:30 - left L: 10min R: 0min\nTotal: 2, Left: 10min, Right: 15min, Bottle: 0.00oz", RightSide, previousTime.Add(3*time.Hour).In(loc).Format("Jan 2 03:04PM"))
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestUserLookup(t *testing.T) {
	fdb := &fakeDynamodb{}
	builder := expression.NewBuilder()
	key := expression.Key("number").Equal(expression.Value("+1234567890"))
	proj := expression.NamesList(expression.Name("id"))
	expr, err := builder.WithKeyCondition(key).WithProjection(proj).Build()
	assert.Nil(t, err)
	qi := &dynamodb.QueryInput{
		TableName:                 aws.String("user"),
		Limit:                     aws.Int64(1),
		ScanIndexForward:          aws.Bool(false),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		ProjectionExpression:      expr.Projection(),
		KeyConditionExpression:    expr.KeyCondition(),
	}
	fdb.On("Query", qi).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{{"id": {N: aws.String("1")}}}}, nil).Once()

	bl := BabyLogger{
		config:   &Config{UserTableName: "user"},
		dynamodb: fdb,
	}

	err = bl.userLookup("+1234567890")
	assert.Nil(t, err)
	assert.Equal(t, int64(1), bl.userid)

	fdb.AssertExpectations(t)
}
