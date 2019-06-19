#!/bin/sh
a=0
rm -rf sep-2.out
touch sep-2.out
while [ $a -lt 100 ]
do
    echo $a 
    python3 start.py
    sleep 300
    printf "\n\n\n" >> sep-2.out 
    echo $a >> sep-2.out
    python3 grep.py "wb not ready|BlockNum=[0-9]*0\ " >> sep-2.out
    sleep 1
    a=`expr $a + 1`
done
