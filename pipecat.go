package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/codegangsta/cli"
	"github.com/streadway/amqp"
)

func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
		panic(fmt.Sprintf("%s: %s", msg, err))
	}
}

func prepare(amqpUri string, queueName string) (*amqp.Connection, *amqp.Channel) {
	conn, err := amqp.Dial(amqpUri)
	failOnError(err, "Failed to connect to AMPQ broker")

	channel, err := conn.Channel()
	failOnError(err, "Failed to open a channel")

	_, err = channel.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
	failOnError(err, "Failed to declare queue")

	return conn, channel
}

func publish(c *cli.Context) {
	queueName := c.Args().First()
	if queueName == "" {
		fmt.Println("Please provide name of the queue")
		os.Exit(1)
	}

	conn, channel := prepare(c.String("amqpuri"), queueName)
	defer conn.Close()
	defer channel.Close()

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		err := channel.Publish(
			"",        // exchange
			queueName, // routing key
			false,     // mandatory
			false,     // immediate
			amqp.Publishing{
				ContentType: "text/plain",
				Body:        []byte(line),
			})

		failOnError(err, "Failed to publish a message")
		fmt.Println(line)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "Reading standard input:", err)
	}
}

func consume(c *cli.Context) {
	queueName := c.Args().First()
	if queueName == "" {
		fmt.Println("Please provide name of the queue")
		os.Exit(1)
	}

	conn, channel := prepare(c.String("amqpuri"), queueName)
	defer conn.Close()
	defer channel.Close()

	var mutex sync.Mutex
	unackedMessages := make([]amqp.Delivery, 100)

	msgs, err := channel.Consume(
		queueName,         // queue
		"",                // consumer
		c.Bool("autoack"), // auto-ack
		false,             // exclusive
		false,             // no-local
		false,             // no-wait
		nil,               // args
	)
	failOnError(err, "Failed to register a consumer")

	ackMessages := func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			ackedLine := scanner.Text()

			// O(n²) complexity for the win!
			mutex.Lock()
			for i, msg := range unackedMessages {
				unackedLine := fmt.Sprintf("%s", msg.Body)
				if unackedLine == ackedLine {
					msg.Ack(false)

					// discard message
					unackedMessages = append(unackedMessages[:i], unackedMessages[i+1:]...)
					break
				}
			}
			mutex.Unlock()

		}
		if err := scanner.Err(); err != nil {
			fmt.Fprintln(os.Stderr, "Reading standard input:", err)
		}
	}

	consumeMessages := func() {
		for msg := range msgs {

			if !c.Bool("autoack") {
				mutex.Lock()
				unackedMessages = append(unackedMessages, msg)
				mutex.Unlock()

			}

			line := fmt.Sprintf("%s", msg.Body)
			fmt.Println(line)
		}
	}

	forever := make(chan bool)
	if c.Bool("autoack") {
		go consumeMessages()
	} else {
		go ackMessages()
		go consumeMessages()
	}
	<-forever
}

func main() {
	app := cli.NewApp()
	app.Name = "pipecat"
	app.Usage = "Connect unix pipes and message queues"

	globalFlags := []cli.Flag{
		cli.StringFlag{
			Name:   "amqpuri",
			Value:  "amqp://guest:guest@localhost:5672/",
			Usage:  "AMQP URI",
			EnvVar: "AMQP_URI",
		},
		cli.BoolFlag{
			Name:  "autoack",
			Usage: "Ack all received messages directly",
		},
	}

	app.Commands = []cli.Command{
		{
			Name:    "publish",
			Aliases: []string{"p"},
			Usage:   "Publish messages to queue",
			Flags:   globalFlags,
			Action:  publish,
		},
		{
			Name:    "consume",
			Flags:   globalFlags,
			Aliases: []string{"c"},
			Usage:   "Consume messages from queue",
			Action:  consume,
		},
	}

	app.Run(os.Args)
}
