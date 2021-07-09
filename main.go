package main

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/aws/aws-sdk-go/service/dynamodb/expression"
	"github.com/b-b3rn4rd/aws-lambda-runtime-golang/pkg/runtime"
	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"
)

const (
	LeftSide  = "left"
	RightSide = "right"
	// BottleSide        = "bottle"
	LatestFeedRequest = "next"
	NewFeedRequest    = "new"
	UpdateFeedRequest = "update"
	NewDiaperRequest  = "diaper"
)

type Config struct {
	FeedingTableName string
	FeedingInterval  time.Duration
	DiaperTableName  string
}

func main() {
	c := Config{}
	err := envconfig.Process("BABYLOGGER", &c)
	if err != nil {
		log.Fatal(err.Error())
	}
	log.WithFields(log.Fields{
		"feeding_table":    c.FeedingTableName,
		"feeding_interval": c.FeedingInterval,
		"diaper_table":     c.DiaperTableName}).
		Info("config loaded")

	s := session.Must(session.NewSession())
	db := dynamodb.New(s)

	blh := BabyLogger{
		config:   &c,
		dynamodb: db,
	}

	runtime.Start(blh.Router)
}

type dynamodber interface {
	PutItem(*dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error)
	UpdateItem(*dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error)
	Query(*dynamodb.QueryInput) (*dynamodb.QueryOutput, error)
}

type BabyLogger struct {
	config   *Config
	dynamodb dynamodber
}

func (b *BabyLogger) Router(req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	log.WithFields(log.Fields{
		"headers":      req.Headers,
		"body":         req.Body,
		"query_string": req.QueryStringParameters,
		"method":       req.HTTPMethod}).Info("incoming request")

	if req.QueryStringParameters == nil {
		return clientError(http.StatusBadRequest)
	}

	switch req.HTTPMethod {
	case "GET":
		message := strings.ToLower(req.QueryStringParameters["Body"])
		if strings.Contains(message, LatestFeedRequest) {
			return b.NextFeed()
		} else if strings.Contains(message, NewFeedRequest) {
			return b.NewFeed(message)
		} else if strings.Contains(message, UpdateFeedRequest) {
			return b.UpdateFeed(message)
		} else if strings.Contains(message, NewDiaperRequest) {
			return b.NewDiaper(message)
		}
		return clientError(http.StatusBadRequest)
	default:
		return clientError(http.StatusMethodNotAllowed)
	}
}

func serverError(err error) (events.APIGatewayProxyResponse, error) {
	log.WithField("error", err.Error()).Error("server error")

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       http.StatusText(http.StatusInternalServerError),
	}, nil
}

func clientError(status int) (events.APIGatewayProxyResponse, error) {
	log.Error("client error")
	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       http.StatusText(status),
	}, nil
}

type Response struct {
	XMLName xml.Name `xml:"Response"`
	Message string   `xml:"Message"`
}

type FeedingRecord struct {
	Date  string `json:"date"`
	Start string `json:"start"`
	Side  string `json:"side"`
}

func getLast(tableName string, db dynamodber) (*dynamodb.QueryOutput, error) {
	date := time.Now().UTC()
	for i := 1; i < 5; i++ {
		key := expression.KeyEqual(expression.Key("date"), expression.Value(date.Format("2006-01-02")))
		proj := expression.NamesList(expression.Name("date"), expression.Name("start"), expression.Name("side"))
		expr, err := expression.NewBuilder().WithKeyCondition(key).WithProjection(proj).Build()
		if err != nil {
			return nil, err
		}

		qi := &dynamodb.QueryInput{
			TableName:                 aws.String(tableName),
			Limit:                     aws.Int64(1),
			ScanIndexForward:          aws.Bool(false),
			ExpressionAttributeNames:  expr.Names(),
			ExpressionAttributeValues: expr.Values(),
			ProjectionExpression:      expr.Projection(),
			KeyConditionExpression:    expr.KeyCondition(),
		}

		o, err := db.Query(qi)
		if err != nil {
			return nil, err
		}
		log.WithField("output", o.String()).Info("dynamodb query succeeded")

		if len(o.Items) != 1 {
			date = date.AddDate(0, 0, -1)
			continue
		}
		return o, nil
	}
	return nil, nil
}

// NextFeed - Gets the latest feeding and responds with expected next feeding and which side
func (b *BabyLogger) NextFeed() (events.APIGatewayProxyResponse, error) {
	output, err := getLast(b.config.FeedingTableName, b.dynamodb)
	if err != nil {
		return serverError(err)
	}
	if output == nil {
		log.Error("no data found")
		return clientError(http.StatusNotFound)
	}

	var fr FeedingRecord
	err = dynamodbattribute.UnmarshalMap(output.Items[0], &fr)
	if err != nil {
		return serverError(err)
	}

	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return serverError(err)
	}
	previousTime, err := time.Parse("2006-01-02T15:04:05", fmt.Sprintf("%sT%s:00", fr.Date, fr.Start))
	if err != nil {
		return serverError(err)
	}

	xmlResp := &Response{}
	nextTime := previousTime.Add(b.config.FeedingInterval).In(loc)
	var nextSide string
	if fr.Side == LeftSide {
		nextSide = RightSide
	} else {
		nextSide = LeftSide
	}
	xmlResp.Message = fmt.Sprintf("The next feeding is on your %s side on %s", nextSide, nextTime.Format("Jan 2 03:04PM"))

	resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
	if err != nil {
		return serverError(err)
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(resp),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, nil
}

// func match(start, end, s string) string {
//     i := strings.Index(s, start)
//     if i >= 0 {
//         j := strings.Index(s[i:], end)
//         if j >= 0 {
//             return s[i+len(start) : i+j]
//         }
//     }
//     return ""
// }

func (b *BabyLogger) NewFeed(message string) (events.APIGatewayProxyResponse, error) {
	current := time.Now().UTC()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return serverError(err)
	}

	re := regexp.MustCompile(`^new (?P<side>[A-Za-z]+).*`)
	match := re.FindStringSubmatch(message)
	index := re.SubexpIndex("side")
	side := match[index]
	if side != LeftSide && side != RightSide {
		return clientError(http.StatusBadRequest)
	}

	var d string
	var dateIncluded bool
	d, dateIncluded, err = getDate(message, current)
	if err != nil {
		return serverError(err)
	}

	var t string
	var twentyFourHourTime bool
	t, d, twentyFourHourTime, current, err = getTime(message, d, dateIncluded, current, loc)
	if err != nil {
		return serverError(err)
	}

	leftRE := regexp.MustCompile(`.*left (?P<left>\d+){0,1}.*`)
	leftMatch := leftRE.FindStringSubmatch(message)
	leftIndex := leftRE.SubexpIndex("left")
	var left string
	if len(leftMatch) > 0 {
		left = leftMatch[leftIndex]
	}

	rightRE := regexp.MustCompile(`.*right (?P<right>\d+){0,1}.*`)
	rightMatch := rightRE.FindStringSubmatch(message)
	rightIndex := rightRE.SubexpIndex("right")
	var right string
	if len(rightMatch) > 0 {
		right = rightMatch[rightIndex]
	}

	i := &dynamodb.PutItemInput{
		TableName: aws.String(b.config.FeedingTableName),
		Item: map[string]*dynamodb.AttributeValue{
			"date": {
				S: aws.String(d),
			},
			"start": {
				S: aws.String(t),
			},
			"side": {
				S: aws.String(side),
			},
		},
	}
	if left != "" {
		i.Item["leftDuration"] = &dynamodb.AttributeValue{N: aws.String(left)}
	}
	if right != "" {
		i.Item["rightDuration"] = &dynamodb.AttributeValue{N: aws.String(right)}
	}
	o, err := b.dynamodb.PutItem(i)
	if err != nil {
		return serverError(err)
	}
	log.WithField("output", o).Info("dynamodb put succeeded")

	xmlResp := &Response{}
	if twentyFourHourTime {
		xmlResp.Message = fmt.Sprintf("New feeding recorded on %s starting on %s side", current.In(loc).Format("Jan 2 15:04"), side)
	} else {
		xmlResp.Message = fmt.Sprintf("New feeding recorded on %s starting on %s side", current.In(loc).Format("Jan 2 03:04PM"), side)
	}

	resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
	if err != nil {
		return serverError(err)
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusCreated,
		Body:       string(resp),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, nil
}

func (b *BabyLogger) UpdateFeed(message string) (events.APIGatewayProxyResponse, error) {
	current := time.Now().UTC()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return serverError(err)
	}

	re := regexp.MustCompile(`^update last.*`)
	match := re.FindStringSubmatch(message)
	var d string
	var dateIncluded bool
	var t string
	var twentyFourHourTime bool
	if len(match) != 0 {
		output, err := getLast(b.config.FeedingTableName, b.dynamodb)
		if err != nil {
			return serverError(err)
		}
		if output == nil {
			log.Error("no data found")
			return clientError(http.StatusNotFound)
		}

		var fr FeedingRecord
		err = dynamodbattribute.UnmarshalMap(output.Items[0], &fr)
		if err != nil {
			return serverError(err)
		}

		d = fr.Date
		t = fr.Start

		// loc, err := time.LoadLocation("America/Los_Angeles")
		// if err != nil {
		// 	return serverError(err)
		// }
		current, err = time.Parse("2006-01-02T15:04:05", fmt.Sprintf("%sT%s:00", fr.Date, fr.Start))
		if err != nil {
			return serverError(err)
		}
	} else {

		d, dateIncluded, err = getDate(message, current)
		if err != nil {
			return serverError(err)
		}

		t, d, twentyFourHourTime, current, err = getTime(message, d, dateIncluded, current, loc)
		if err != nil {
			return serverError(err)
		}
	}

	cond := expression.Equal(expression.Name("start"), expression.Value(t))
	exprBuilder := expression.NewBuilder().WithCondition(cond)

	leftRE := regexp.MustCompile(`.*left (?P<left>\d+){0,1}.*`)
	leftMatch := leftRE.FindStringSubmatch(message)
	leftIndex := leftRE.SubexpIndex("left")
	var left string
	if len(leftMatch) > 0 {
		left = leftMatch[leftIndex]
		exprBuilder.WithUpdate(expression.Set(expression.Name("leftDuration"), expression.Value(left)))
	}

	rightRE := regexp.MustCompile(`.*right (?P<right>\d+){0,1}.*`)
	rightMatch := rightRE.FindStringSubmatch(message)
	rightIndex := rightRE.SubexpIndex("right")
	var right string
	if len(rightMatch) > 0 {
		right = rightMatch[rightIndex]
		exprBuilder.WithUpdate(expression.Set(expression.Name("rightDuration"), expression.Value(right)))
	}

	if left == "" && right == "" {
		return clientError(http.StatusBadRequest)
	}

	expr, err := exprBuilder.Build()
	if err != nil {
		fmt.Println(expr)
		fmt.Println(err)
		return serverError(err)
	}

	i := &dynamodb.UpdateItemInput{
		TableName: aws.String(b.config.FeedingTableName),
		Key: map[string]*dynamodb.AttributeValue{
			"date": {
				S: aws.String(d),
			},
			"start": {
				S: aws.String(t),
			},
		},
		ExpressionAttributeValues: expr.Values(),
		ExpressionAttributeNames:  expr.Names(),
		ConditionExpression:       expr.Condition(),
		ReturnValues:              aws.String("UPDATED_NEW"),
		UpdateExpression:          expr.Update(),
	}

	o, err := b.dynamodb.UpdateItem(i)
	if err != nil {
		return serverError(err)
	}
	log.WithField("output", o).Info("dynamodb put succeeded")

	xmlResp := &Response{}
	if twentyFourHourTime {
		xmlResp.Message = fmt.Sprintf("Updated feeding recorded on %s", current.In(loc).Format("Jan 2 15:04"))
	} else {
		xmlResp.Message = fmt.Sprintf("Updated feeding recorded on %s", current.In(loc).Format("Jan 2 03:04PM"))
	}

	resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
	if err != nil {
		return serverError(err)
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(resp),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, nil
}

func (b *BabyLogger) NewDiaper(message string) (events.APIGatewayProxyResponse, error) {
	current := time.Now().UTC()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return serverError(err)
	}

	var d string
	var dateIncluded bool
	d, dateIncluded, err = getDate(message, current)
	if err != nil {
		return serverError(err)
	}

	var t string
	var twentyFourHourTime bool
	t, d, twentyFourHourTime, current, err = getTime(message, d, dateIncluded, current, loc)
	if err != nil {
		return serverError(err)
	}

	var wet, soiled bool
	if strings.Contains(message, "wet") {
		wet = true
	}
	if strings.Contains(message, "soiled") {
		soiled = true
	}

	checkedRE := regexp.MustCompile(`.*checked (?P<checked>[A-Za-z]+-{0,1}[A-Za-z]*){0,1}.*`)
	checkedMatch := checkedRE.FindStringSubmatch(message)
	checkedIndex := checkedRE.SubexpIndex("checked")
	var checked string
	if len(checkedMatch) > 0 {
		checked = checkedMatch[checkedIndex]
	}

	i := &dynamodb.PutItemInput{
		TableName: aws.String(b.config.DiaperTableName),
		Item: map[string]*dynamodb.AttributeValue{
			"date": {
				S: aws.String(d),
			},
			"time": {
				S: aws.String(t),
			},
			"wet": {
				BOOL: aws.Bool(wet),
			},
			"soiled": {
				BOOL: aws.Bool(soiled),
			},
		},
	}
	if checked != "" {
		i.Item["checked"] = &dynamodb.AttributeValue{S: aws.String(checked)}
	}
	o, err := b.dynamodb.PutItem(i)
	if err != nil {
		return serverError(err)
	}
	log.WithField("output", o).Info("dynamodb put succeeded")

	xmlResp := &Response{}
	if twentyFourHourTime {
		xmlResp.Message = fmt.Sprintf("New diaper recorded on %s", current.In(loc).Format("Jan 2 15:04"))
	} else {
		xmlResp.Message = fmt.Sprintf("New diaper recorded on %s", current.In(loc).Format("Jan 2 03:04PM"))
	}

	resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
	if err != nil {
		return serverError(err)
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusCreated,
		Body:       string(resp),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, nil
}

func getDate(message string, current time.Time) (string, bool, error) {
	var d string
	var dateIncluded bool
	if strings.Contains(message, "date") {
		re := regexp.MustCompile(`.*date (?P<date>\d{4}-\d{2}-\d{2}).*`)
		match := re.FindStringSubmatch(message)
		index := re.SubexpIndex("date")
		d = match[index]
		if d == "" {
			return "", false, fmt.Errorf("date format is invalid")
		}
		dateIncluded = true
	} else {
		d = current.Format("2006-01-02")
	}

	return d, dateIncluded, nil
}

func getTime(message, d string, dateIncluded bool, current time.Time, loc *time.Location) (string, string, bool, time.Time, error) {
	var t string
	var twentyFourHourTime bool
	var err error
	if strings.Contains(message, "time") {
		if !dateIncluded {
			d = time.Now().In(loc).Format("2006-01-02")
		}
		re := regexp.MustCompile(`.*time (?P<time>\d{1,2}:\d{2})\s*(?P<meridiem>(am|pm)){0,1}.*`)
		match := re.FindStringSubmatch(message)
		timeIndex := re.SubexpIndex("time")
		timeValue := match[timeIndex]
		meridiemIndex := re.SubexpIndex("meridiem")
		meridiemValue := match[meridiemIndex]
		if meridiemValue == "" {
			twentyFourHourTime = true
			current, err = time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %s", d, timeValue), loc)
			if err != nil {
				return "", "", false, time.Time{}, err
			}
		} else {
			current, err = time.ParseInLocation("2006-01-02 03:04 PM", fmt.Sprintf("%s %s %s", d, timeValue, strings.ToUpper(meridiemValue)), loc)
			if err != nil {
				return "", "", false, time.Time{}, err
			}
		}
		t = current.UTC().Format("15:04")
	} else {
		t = current.Format("15:04")
	}
	return t, d, twentyFourHourTime, current, nil
}
