package main

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
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
	outputSide := "left"
	fdb := &fakeDynamodb{}
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{}, nil).Once()
	fdb.On("Query", mock.Anything).Return(&dynamodb.QueryOutput{Items: []map[string]*dynamodb.AttributeValue{
		{"date": &dynamodb.AttributeValue{S: aws.String(outputDate)}, "start": &dynamodb.AttributeValue{S: aws.String(outputTime)}, "side": &dynamodb.AttributeValue{S: aws.String(outputSide)}},
	}}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding", FeedingInterval: 3 * time.Hour},
		dynamodb: fdb,
	}

	resp, err := bl.NextFeed()
	assert.Nil(t, err)

	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	previousTime, err := time.Parse("2006-01-02T15:04:05", fmt.Sprintf("%sT%s:00", outputDate, outputTime))
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

func TestNewFeed(t *testing.T) {
	current := time.Now().UTC()
	fdb := &fakeDynamodb{}
	fdb.On("PutItem", &dynamodb.PutItemInput{TableName: aws.String("feeding"), Item: map[string]*dynamodb.AttributeValue{
		"date": {
			S: aws.String(current.Format("2006-01-02")),
		},
		"start": {
			S: aws.String(current.Format("15:04")),
		},
		"side": {
			S: aws.String("left"),
		},
	}}).Return(&dynamodb.PutItemOutput{}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding"},
		dynamodb: fdb,
	}

	message := "new left"
	resp, err := bl.NewFeed(message)
	assert.Nil(t, err)

	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("New feeding recorded on %s starting on %s side", current.In(loc).Format("Jan 2 03:04PM"), "left")
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusCreated,
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
	fdb.On("PutItem", &dynamodb.PutItemInput{TableName: aws.String("feeding"), Item: map[string]*dynamodb.AttributeValue{
		"date": {
			S: aws.String(d),
		},
		"start": {
			S: aws.String(tiTime.UTC().Format("15:04")),
		},
		"side": {
			S: aws.String("right"),
		},
	}}).Return(&dynamodb.PutItemOutput{}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding"},
		dynamodb: fdb,
	}

	message := "new right date 2010-01-01 time 01:05 am"
	resp, err := bl.NewFeed(message)
	assert.Nil(t, err)

	datetime, err := time.Parse("2006-01-02T15:04:05", fmt.Sprintf("%sT%s:00", d, tiTime.Format("15:04")))
	assert.Nil(t, err)

	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("New feeding recorded on %s starting on %s side", datetime.Format("Jan 2 03:04PM"), "right")
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusCreated,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}

func TestNewFeed_DateAndTime_24H(t *testing.T) {
	d := "2010-01-01"
	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	tiTime, err := time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s 20:05", d), loc)
	assert.Nil(t, err)
	fdb := &fakeDynamodb{}
	fdb.On("PutItem", &dynamodb.PutItemInput{TableName: aws.String("feeding"), Item: map[string]*dynamodb.AttributeValue{
		"date": {
			S: aws.String(d),
		},
		"start": {
			S: aws.String(tiTime.UTC().Format("15:04")),
		},
		"side": {
			S: aws.String("left"),
		},
	}}).Return(&dynamodb.PutItemOutput{}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding"},
		dynamodb: fdb,
	}

	message := "new left date 2010-01-01 time 20:05"
	resp, err := bl.NewFeed(message)
	assert.Nil(t, err)

	datetime, err := time.Parse("2006-01-02 15:04", fmt.Sprintf("%s %s", d, tiTime.Format("15:04")))
	assert.Nil(t, err)

	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("New feeding recorded on %s starting on %s side", datetime.Format("Jan 2 15:04"), "left")
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusCreated,
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
	fdb.On("PutItem", &dynamodb.PutItemInput{TableName: aws.String("feeding"), Item: map[string]*dynamodb.AttributeValue{
		"date": {
			S: aws.String(current.Format("2006-01-02")),
		},
		"start": {
			S: aws.String(current.Format("15:04")),
		},
		"side": {
			S: aws.String("left"),
		},
		"leftDuration": {
			N: aws.String("15"),
		},
		"rightDuration": {
			N: aws.String("10"),
		},
	}}).Return(&dynamodb.PutItemOutput{}, nil).Once()
	bl := BabyLogger{
		config:   &Config{FeedingTableName: "feeding"},
		dynamodb: fdb,
	}

	message := "new left left 15 right 10"
	resp, err := bl.NewFeed(message)
	assert.Nil(t, err)

	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.Nil(t, err)
	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("New feeding recorded on %s starting on %s side", current.In(loc).Format("Jan 2 03:04PM"), "left")
	expectedBody, err := xml.MarshalIndent(xmlResp, " ", "  ")
	assert.Nil(t, err)
	assert.Equal(t, events.APIGatewayProxyResponse{
		StatusCode: http.StatusCreated,
		Body:       string(expectedBody),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, resp)

	fdb.AssertExpectations(t)
}
